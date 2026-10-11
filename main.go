package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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

func main() {
	listenAddr := env("LISTEN_ADDR", "0.0.0.0:25665")
	remoteAddr := env("REMOTE_ADDR", "donutsmp.net:19132")
	resourceDir := env("RESOURCE_PACK_DIR", "resource_packs")

	// Ensure resource directory exists (FeatherPanel expects it next to the server binary)
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		log.Fatalf("create resource pack dir %s: %v", resourceDir, err)
	}

	// Discover resource packs at startup and register with the listener via resource.Packs
	packs := discoverPacks(resourceDir)
	log.Printf("Nova Bedrock direct proxy")
	log.Printf("Listening on UDP %s; upstream %s", listenAddr, remoteAddr)
	if len(packs) == 0 {
		log.Printf("Resource packs: none found in %s; behaving as transparent upstream passthrough.", resourceDir)
	} else {
		log.Printf("Resource packs: %d pack(s) discovered in %s", len(packs), resourceDir)
		for i, p := range packs {
			log.Printf("  %d: %s (%s) size=%d hash=%s", i+1, p.Name(), p.UUID().String(), p.Len(), hex.EncodeToString(p.Hash()))
		}
	}

	token, err := auth.RequestLiveToken()
	if err != nil {
		log.Fatalf("Microsoft/Xbox authentication setup failed: %v", err)
	}
	src := auth.RefreshTokenSource(token)

	status, err := minecraft.NewForeignStatusProvider(remoteAddr)
	cfg := minecraft.ListenConfig{StatusProvider: status}
	// Attach discovered resource packs to the listener config so they will be offered to clients.
	cfg.ResourcePacks = packs
	listener, err := cfg.Listen("raknet", listenAddr)
	if err != nil {
		log.Fatalf("create upstream status provider: %v", err)
	}
	listener, err := (minecraft.ListenConfig{StatusProvider: status}).Listen("raknet", listenAddr)
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
	// Avoid accidentally binding a TCP-only port from a panel allocation.
	_ = net.IPv4zero
}
