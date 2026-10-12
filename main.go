package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sandertv/gophertunnel/minecraft/resource"
	"golang.org/x/oauth2"
)

var active atomic.Int64
var protocolLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

const upstreamDialTimeout = 20 * time.Second
const upstreamResolveTimeout = 5 * time.Second

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func executableDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func resolveResourcePackDir() string {
	dir := env("RESOURCE_PACK_DIR", filepath.Join(executableDir(), "resource_packs"))
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(executableDir(), dir)
}

func resolveAuthTokenPath() string {
	path := env("AUTH_TOKEN_FILE", ".novabedrock-auth.json")
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(executableDir(), path)
}

func discoverPacks(dir string) []*resource.Pack {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("resource pack directory creation failed: %v", err)
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("resource pack directory read failed: %v", err)
		return nil
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".mcpack") || strings.HasSuffix(name, ".zip") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)

	packs := make([]*resource.Pack, 0, len(paths))
	seenUUIDs := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pack, err := resource.ReadPath(path)
		if err != nil {
			log.Printf("resource pack load failed for %s: %v", filepath.Base(path), err)
			continue
		}
		uuid := pack.UUID().String()
		if _, ok := seenUUIDs[uuid]; ok {
			log.Printf("resource pack load failed for %s: duplicate pack UUID %s", filepath.Base(path), uuid)
			continue
		}
		seenUUIDs[uuid] = struct{}{}
		packs = append(packs, pack)
		log.Printf("loaded resource pack: %s (%s v%s)", pack.Name(), uuid, pack.Version())
	}
	return packs
}

func main() {
	if err := run(); err != nil {
		log.Printf("proxy stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddr := env("LISTEN_ADDR", "0.0.0.0:25665")
	remoteAddr := env("REMOTE_ADDR", "play.lbsg.net:19132")
	if _, _, err := splitHostPort(remoteAddr); err != nil {
		return fmt.Errorf("invalid REMOTE_ADDR: %w", err)
	}
	commandPrefix := env("COMMAND_PREFIX", ".")
	if strings.TrimSpace(commandPrefix) == "" || strings.ContainsAny(commandPrefix, " \t\r\n") {
		return errors.New("COMMAND_PREFIX must be non-empty and contain no whitespace")
	}
	forwardUnknownCommands, err := strconv.ParseBool(env("FORWARD_UNKNOWN_PREFIX_COMMANDS", "true"))
	if err != nil {
		return fmt.Errorf("invalid FORWARD_UNKNOWN_PREFIX_COMMANDS value: %w", err)
	}
	scannerEnabled, err := strconv.ParseBool(env("SCANNER_ENABLED", "true"))
	if err != nil {
		return fmt.Errorf("invalid SCANNER_ENABLED value: %w", err)
	}
	scannerCacheLimit, err := strconv.Atoi(env("SCANNER_CACHE_LIMIT", "256"))
	if err != nil {
		return fmt.Errorf("invalid SCANNER_CACHE_LIMIT value: %w", err)
	}
	diagnostics, err := newDiagnosticsTracker(scannerEnabled, scannerCacheLimit)
	if err != nil {
		return fmt.Errorf("configure scanner diagnostics: %w", err)
	}
	resourceDir := resolveResourcePackDir()
	packs := discoverPacks(resourceDir)
	commands, err := newCommandEngine(commandPrefix, forwardUnknownCommands, listenAddr, remoteAddr, packs, diagnostics)
	if err != nil {
		return fmt.Errorf("configure chat commands: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Nova Bedrock direct proxy")
	log.Printf("Listening address: UDP %s; upstream address: %s", listenAddr, remoteAddr)
	log.Printf("Resource pack directory: %s; loaded packs: %d", resourceDir, len(packs))
	log.Printf("Microsoft/Xbox authentication: loading persistent credentials")
	authTokenPath := resolveAuthTokenPath()
	log.Printf("Microsoft/Xbox authentication file: %s", authTokenPath)
	log.Printf("Scanner diagnostics: enabled=%t; metadata cache limit=%d", scannerEnabled, scannerCacheLimit)
	tokenSource, err := loadAuthentication(ctx, authTokenPath)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return err
	}
	log.Printf("Microsoft/Xbox authentication: ready (persistent token storage enabled)")

	cfg := minecraft.ListenConfig{
		ErrorLog:             protocolLogger,
		StatusProvider:       minecraft.NewStatusProvider("Nova Bedrock Proxy", "Nova Bedrock Proxy"),
		ResourcePacks:        packs,
		TexturePacksRequired: len(packs) > 0,
	}
	listener, err := cfg.Listen("raknet", listenAddr)
	if err != nil {
		return fmt.Errorf("listen on UDP %s (check that FeatherPanel allocated this UDP port): %w", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("Proxy is listening on %s.", listener.Addr())
	go runConsoleCommands(os.Stdin, os.Stdout, commands, active.Load)

	var connections sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		c, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("accept incoming connection failed: %v", err)
			time.Sleep(time.Second)
			continue
		}
		conn, ok := c.(*minecraft.Conn)
		if !ok {
			_ = c.Close()
			continue
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			handleConn(ctx, conn, listener, remoteAddr, tokenSource, commands, diagnostics)
		}()
	}
	connections.Wait()
	log.Printf("Proxy shutdown complete.")
	return nil
}

func splitHostPort(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", fmt.Errorf("expected host:port: %w", err)
	}
	if host == "" {
		return "", "", errors.New("host must not be empty")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", errors.New("port must be a number between 1 and 65535")
	}
	return host, port, nil
}

func dialUpstream(ctx context.Context, dialer minecraft.Dialer, address string) (*minecraft.Conn, error) {
	host, port, err := splitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream address: %w", err)
	}
	resolveCtx, cancelResolve := context.WithTimeout(ctx, upstreamResolveTimeout)
	ips, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	cancelResolve()
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) || net.ParseIP(host) == nil {
			return nil, fmt.Errorf("DNS resolution failed for upstream host %q: %w", host, err)
		}
		return nil, fmt.Errorf("resolve upstream host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("DNS resolution failed for upstream host %q: no addresses returned", host)
	}

	var lastErr error
	for _, ip := range ips {
		dialCtx, cancelDial := context.WithTimeout(ctx, upstreamDialTimeout)
		conn, dialErr := dialer.DialContext(dialCtx, "raknet", net.JoinHostPort(ip.String(), port))
		cancelDial()
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		if ctx.Err() != nil {
			break
		}
	}
	var upstreamDisconnect minecraft.DisconnectError
	if isConnectionRefused(lastErr) || errors.As(lastErr, &upstreamDisconnect) {
		return nil, fmt.Errorf("upstream UDP connection was refused or rejected: %w", lastErr)
	}
	if isTimeout(lastErr) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("upstream UDP connection timed out: %w", lastErr)
	}
	if lastErr == nil && ctx.Err() != nil {
		return nil, fmt.Errorf("upstream connection canceled: %w", ctx.Err())
	}
	return nil, fmt.Errorf("upstream UDP connection failed: %w", lastErr)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

func handleConn(ctx context.Context, conn *minecraft.Conn, listener *minecraft.Listener, remoteAddr string, src oauth2.TokenSource, commands *commandEngine, diagnostics *diagnosticsTracker) {
	active.Add(1)
	defer active.Add(-1)
	playerName := conn.IdentityData().DisplayName
	if playerName == "" {
		playerName = "unnamed player"
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, upstreamDialTimeout+upstreamResolveTimeout)
	remote, err := dialUpstream(dialCtx, minecraft.Dialer{
		ErrorLog:    protocolLogger,
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}, remoteAddr)
	cancelDial()
	if err != nil {
		log.Printf("upstream connection failed for %s: %v", playerName, err)
		_ = listener.Disconnect(conn, "Could not connect to the destination server.")
		_ = conn.Close()
		return
	}
	defer remote.Close()

	spawnCtx, cancelSpawn := context.WithCancel(ctx)
	defer cancelSpawn()
	spawnErrors := make(chan error, 2)
	go func() {
		spawnErrors <- conn.StartGameContext(spawnCtx, remote.GameData())
	}()
	go func() {
		spawnErrors <- remote.DoSpawnContext(spawnCtx)
	}()
	for range 2 {
		if err := <-spawnErrors; err != nil {
			cancelSpawn()
			var disconnect minecraft.DisconnectError
			if errors.As(err, &disconnect) {
				log.Printf("upstream rejected the connection for %s: %s", playerName, disconnect)
			} else {
				log.Printf("spawn handshake failed for %s: %s", playerName, redactPlayerAddress(err, conn.RemoteAddr()))
			}
			_ = listener.Disconnect(conn, "Could not complete the connection to the destination server.")
			_ = remote.Close()
			return
		}
	}

	connectedAt := time.Now()
	log.Printf("player %s connected; upstream session established", playerName)

	done := make(chan relayResult, 2)
	go func() {
		var relayErr error
		for {
			pk, err := conn.ReadPacket()
			if err != nil {
				relayErr = err
				break
			}
			diagnostics.observe("client->upstream", pk)
			if text, ok := pk.(*packet.Text); ok && text.TextType == packet.TextTypeChat {
				if response, handled := commands.handle(text.Message, active.Load()); handled {
					if response != "" {
						if err := writeCommandResponse(conn, response); err != nil {
							relayErr = err
							break
						}
					}
					continue
				}
			}
			if err := remote.WritePacket(pk); err != nil {
				relayErr = err
				break
			}
		}
		done <- relayResult{direction: "client-to-upstream", err: relayErr}
	}()
	go func() {
		var relayErr error
		for {
			pk, err := remote.ReadPacket()
			if err != nil {
				relayErr = err
				break
			}
			diagnostics.observe("upstream->client", pk)
			if err := conn.WritePacket(pk); err != nil {
				relayErr = err
				break
			}
		}
		done <- relayResult{direction: "upstream-to-client", err: relayErr}
	}()

	var result relayResult
	select {
	case result = <-done:
	case <-ctx.Done():
		_ = conn.Close()
		_ = remote.Close()
		<-done
		<-done
		log.Printf("connection for %s closed during proxy shutdown", playerName)
		return
	}
	if result.err != nil {
		var disconnect minecraft.DisconnectError
		if errors.As(result.err, &disconnect) {
			log.Printf("%s relay ended for %s after %s: upstream disconnected: %s", result.direction, playerName, time.Since(connectedAt).Round(time.Second), disconnect)
			_ = listener.Disconnect(conn, disconnect.Error())
		} else {
			log.Printf("%s relay ended for %s after %s: %s", result.direction, playerName, time.Since(connectedAt).Round(time.Second), redactPlayerAddress(result.err, conn.RemoteAddr()))
			_ = listener.Disconnect(conn, "The connection to the destination server was interrupted.")
		}
	} else {
		log.Printf("%s relay ended for %s after %s", result.direction, playerName, time.Since(connectedAt).Round(time.Second))
		_ = listener.Disconnect(conn, "Connection closed.")
	}
	_ = conn.Close()
	_ = remote.Close()
	<-done
}

func redactPlayerAddress(err error, address net.Addr) string {
	message := err.Error()
	if address != nil {
		message = strings.ReplaceAll(message, address.String(), "[client]")
	}
	return message
}

type relayResult struct {
	direction string
	err       error
}
