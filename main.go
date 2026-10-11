package main

import (
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/resource"
	"golang.org/x/oauth2"
)

var active atomic.Int64

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
	resourceDir := resolveResourcePackDir()
	packs := discoverPacks(resourceDir)

	log.Printf("Nova Bedrock direct proxy")
	log.Printf("Listening on UDP %s; upstream %s", listenAddr, remoteAddr)
	log.Printf("Resource pack dir: %s", resourceDir)
	if len(packs) == 0 {
		log.Printf("Resource packs: none discovered. Startup continues with upstream passthrough only.")
	} else {
		log.Printf("Resource packs: %d loaded and ready for Bedrock negotiation.", len(packs))
	}

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
		StatusProvider:       status,
		ResourcePacks:        packs,
		TexturePacksRequired: false,
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
		go handleConn(conn, listener, remoteAddr, src)
	}
}

func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, remoteAddr string, src oauth2.TokenSource) {
	active.Add(1)
	defer active.Add(-1)

	remote, err := (minecraft.Dialer{
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}).Dial("raknet", remoteAddr)
	if err != nil {
		log.Printf("upstream dial failed for %s: %v", conn.RemoteAddr(), err)
		_ = listener.Disconnect(conn, "Could not connect to the destination server.")
		_ = conn.Close()
		return
	}

	var ready sync.WaitGroup
	ready.Add(2)
	go func() {
		defer ready.Done()
		if err := conn.StartGame(remote.GameData()); err != nil {
			log.Printf("client start game: %v", err)
		}
	}()
	go func() {
		defer ready.Done()
		if err := remote.DoSpawn(); err != nil {
			log.Printf("upstream spawn: %v", err)
		}
	}()
	ready.Wait()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		defer remote.Close()
		defer listener.Disconnect(conn, "Connection closed.")
		for {
			pk, err := conn.ReadPacket()
			if err != nil {
				return
			}
			if err := remote.WritePacket(pk); err != nil {
				var disc minecraft.DisconnectError
				if errors.As(err, &disc) {
					_ = listener.Disconnect(conn, disc.Error())
				}
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		defer conn.Close()
		defer remote.Close()
		for {
			pk, err := remote.ReadPacket()
			if err != nil {
				var disc minecraft.DisconnectError
				if errors.As(err, &disc) {
					_ = listener.Disconnect(conn, disc.Error())
				}
				return
			}
			if err := conn.WritePacket(pk); err != nil {
				return
			}
		}
	}()
	<-done
	_ = conn.Close()
	_ = remote.Close()
	<-done
}

func init() {
	_ = net.IPv4zero
}
