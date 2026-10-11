package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sandertv/gophertunnel/minecraft/resource"
	"golang.org/x/oauth2"
)

var active atomic.Int64
var protocolLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	dir := env("RESOURCE_PACK_DIR", "resource_packs")
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(executableDir(), dir)
}

func discoverPacks(dir string) []*resource.Pack {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("resource pack dir: create %s: %v", dir, err)
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("resource pack dir: read %s: %v", dir, err)
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
	for _, path := range paths {
		pack, err := resource.ReadPath(path)
		if err != nil {
			log.Printf("resource pack load failed: %s (%v)", path, err)
			continue
		}
		packs = append(packs, pack)
		log.Printf("loaded resource pack: %s (%s v%s)", pack.Name(), pack.UUID(), pack.Version())
	}
	return packs
}

func main() {
	listenAddr := env("LISTEN_ADDR", "0.0.0.0:25665")
	remoteAddr := env("REMOTE_ADDR", "donutsmp.net:19132")
	commandPrefix := env("COMMAND_PREFIX", ".")
	if strings.TrimSpace(commandPrefix) == "" || strings.ContainsAny(commandPrefix, " \t\r\n") {
		log.Fatal("COMMAND_PREFIX must be non-empty and contain no whitespace")
	}
	forwardUnknownCommands, err := strconv.ParseBool(env("FORWARD_UNKNOWN_PREFIX_COMMANDS", "true"))
	if err != nil {
		log.Fatalf("invalid FORWARD_UNKNOWN_PREFIX_COMMANDS value: %v", err)
	}
	scannerEnabled, err := strconv.ParseBool(env("SCANNER_ENABLED", "true"))
	if err != nil {
		log.Fatalf("invalid SCANNER_ENABLED value: %v", err)
	}
	scannerCacheLimit, err := strconv.Atoi(env("SCANNER_CACHE_LIMIT", "256"))
	if err != nil {
		log.Fatalf("invalid SCANNER_CACHE_LIMIT value: %v", err)
	}
	diagnostics, err := newDiagnosticsTracker(scannerEnabled, scannerCacheLimit)
	if err != nil {
		log.Fatalf("configure scanner diagnostics: %v", err)
	}
	resourceDir := resolveResourcePackDir()
	packs := discoverPacks(resourceDir)
	commands, err := newCommandEngine(commandPrefix, forwardUnknownCommands, listenAddr, remoteAddr, packs, diagnostics)
	if err != nil {
		log.Fatalf("configure chat commands: %v", err)
	}

	log.Printf("Nova Bedrock direct proxy")
	log.Printf("Listening on UDP %s; upstream %s", listenAddr, remoteAddr)
	log.Printf("Resource pack dir: %s", resourceDir)
	log.Printf("Scanner diagnostics: enabled=%t; metadata cache limit=%d", scannerEnabled, scannerCacheLimit)
	if len(packs) == 0 {
		log.Printf("Resource packs: none discovered. Startup continues with upstream passthrough only.")
	} else {
		log.Printf("Resource packs: %d loaded and ready for Bedrock negotiation.", len(packs))
	}
	go runConsoleCommands(os.Stdin, os.Stdout, commands, active.Load)

	token, err := auth.RequestLiveToken()
	if err != nil {
		log.Fatalf("Microsoft/Xbox authentication setup failed: %v", err)
	}
	src := auth.RefreshTokenSource(token)

	status, err := minecraft.NewForeignStatusProvider(remoteAddr)
	if err != nil {
		log.Fatalf("create upstream status provider: %v", err)
	}

	cfg := minecraft.ListenConfig{
		ErrorLog:             protocolLogger,
		StatusProvider:       status,
		ResourcePacks:        packs,
		TexturePacksRequired: len(packs) > 0,
	}
	listener, err := cfg.Listen("raknet", listenAddr)
	if err != nil {
		log.Fatalf("listen on %s (check that FeatherPanel allocated UDP): %v", listenAddr, err)
	}
	defer listener.Close()

	log.Printf("Proxy is listening. Add the allocated public IP and UDP port to Bedrock.")
	for {
		c, err := listener.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			time.Sleep(time.Second)
			continue
		}
		conn, ok := c.(*minecraft.Conn)
		if !ok {
			_ = c.Close()
			continue
		}
		go handleConn(conn, listener, remoteAddr, src, commands, diagnostics)
	}
}

func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, remoteAddr string, src oauth2.TokenSource, commands *commandEngine, diagnostics *diagnosticsTracker) {
	active.Add(1)
	defer active.Add(-1)

	remote, err := (minecraft.Dialer{
		ErrorLog:    protocolLogger,
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}).Dial("raknet", remoteAddr)
	if err != nil {
		log.Printf("upstream dial failed for %s: %v", conn.RemoteAddr(), err)
		_ = listener.Disconnect(conn, "Could not connect to the destination server.")
		_ = conn.Close()
		return
	}
	defer remote.Close()

	spawnCtx, cancelSpawn := context.WithCancel(context.Background())
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
			log.Printf("spawn handshake failed for %s: %v", conn.RemoteAddr(), err)
			_ = listener.Disconnect(conn, "Could not complete the connection to the destination server.")
			_ = remote.Close()
			return
		}
	}

	connectedAt := time.Now()
	log.Printf("player %s connected from %s; upstream session established", conn.IdentityData().DisplayName, conn.RemoteAddr())

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

	result := <-done
	if result.err != nil {
		var disconnect minecraft.DisconnectError
		if errors.As(result.err, &disconnect) {
			log.Printf("%s relay ended for %s after %s: upstream disconnected: %s", result.direction, conn.RemoteAddr(), time.Since(connectedAt).Round(time.Second), disconnect)
			_ = listener.Disconnect(conn, disconnect.Error())
		} else {
			log.Printf("%s relay ended for %s after %s: %v", result.direction, conn.RemoteAddr(), time.Since(connectedAt).Round(time.Second), result.err)
			_ = listener.Disconnect(conn, "The connection to the destination server was interrupted.")
		}
	} else {
		log.Printf("%s relay ended for %s after %s", result.direction, conn.RemoteAddr(), time.Since(connectedAt).Round(time.Second))
		_ = listener.Disconnect(conn, "Connection closed.")
	}
	_ = conn.Close()
	_ = remote.Close()
	<-done
}

type relayResult struct {
	direction string
	err       error
}

func init() {
	_ = net.IPv4zero
}
