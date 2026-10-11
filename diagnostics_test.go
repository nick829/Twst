package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestScannerCommandParsingAndReset(t *testing.T) {
	engine := testCommandEngine(t, true)
	engine.diagnostics.observe("upstream->client", &packet.LevelChunk{
		SubChunkCount: 3,
		RawPayload:    []byte{1, 2, 3},
		CacheEnabled:  true,
	})

	response, handled := engine.handle(".scanner status", 1)
	if !handled || !strings.Contains(response, "LevelChunk 1") || !strings.Contains(response, "payload-bytes=3") {
		t.Fatalf("scanner status did not report observed metadata: handled=%t response=%q", handled, response)
	}
	if !strings.Contains(response, "Chunk payloads are not decoded") {
		t.Fatalf("scanner status must state its data limitation: %q", response)
	}

	response, handled = engine.handle(".scanner reset", 1)
	if !handled || !strings.Contains(response, "reset") {
		t.Fatalf("scanner reset was not handled: handled=%t response=%q", handled, response)
	}
	snapshot := engine.diagnostics.snapshot()
	if snapshot.counts[diagnosticLevelChunk] != 0 || len(snapshot.events) != 0 {
		t.Fatalf("reset left scanner state: counts=%v events=%v", snapshot.counts, snapshot.events)
	}
}

func TestScannerCommandRejectsInvalidArgumentsLocally(t *testing.T) {
	engine := testCommandEngine(t, true)
	for _, message := range []string{".scanner nonsense", ".scanner reset extra"} {
		t.Run(message, func(t *testing.T) {
			response, handled := engine.handle(message, 0)
			if !handled || !strings.Contains(response, "Usage: .scanner") {
				t.Fatalf("invalid scanner command should be handled with usage: handled=%t response=%q", handled, response)
			}
		})
	}
}

func TestScannerCommandWorksFromConsole(t *testing.T) {
	engine := testCommandEngine(t, true)
	var output bytes.Buffer
	runConsoleCommands(strings.NewReader("scanner status\n.scanner reset\n"), &output, engine, func() int64 { return 0 })
	if !strings.Contains(output.String(), "Scanner diagnostics: enabled") || !strings.Contains(output.String(), "reset") {
		t.Fatalf("console did not handle scanner commands: %q", output.String())
	}
}

func TestDiagnosticsTrackerIsConcurrencySafe(t *testing.T) {
	tracker, err := newDiagnosticsTracker(true, 32)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 12
	const observationsPerWorker = 100
	var wait sync.WaitGroup
	wait.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wait.Done()
			for j := 0; j < observationsPerWorker; j++ {
				tracker.observe("client->upstream", &packet.PlayerAuthInput{})
			}
		}()
	}
	wait.Wait()
	snapshot := tracker.snapshot()
	if got, want := snapshot.counts[diagnosticPlayerAuthInput], uint64(workers*observationsPerWorker); got != want {
		t.Fatalf("movement packet count = %d, want %d", got, want)
	}
	if got, want := len(snapshot.events), 32; got != want {
		t.Fatalf("cached event count = %d, want %d", got, want)
	}
}

func TestDiagnosticsTrackerCacheLimits(t *testing.T) {
	tracker, err := newDiagnosticsTracker(true, 2)
	if err != nil {
		t.Fatal(err)
	}
	tracker.observe("upstream->client", &packet.LevelChunk{SubChunkCount: 1})
	tracker.observe("upstream->client", &packet.UpdateBlock{})
	tracker.observe("client->upstream", &packet.MovePlayer{})
	snapshot := tracker.snapshot()
	if got, want := len(snapshot.events), 2; got != want {
		t.Fatalf("cached event count = %d, want %d", got, want)
	}
	if snapshot.events[0].kind != diagnosticUpdateBlock || snapshot.events[1].kind != diagnosticMovePlayer {
		t.Fatalf("cache did not retain the newest events in order: %#v", snapshot.events)
	}
	if snapshot.counts[diagnosticLevelChunk] != 1 || snapshot.counts[diagnosticUpdateBlock] != 1 || snapshot.counts[diagnosticMovePlayer] != 1 {
		t.Fatalf("cache eviction should not affect counters: %v", snapshot.counts)
	}

	uncached, err := newDiagnosticsTracker(true, 0)
	if err != nil {
		t.Fatal(err)
	}
	uncached.observe("client->upstream", &packet.PlayerAuthInput{})
	if got := len(uncached.snapshot().events); got != 0 {
		t.Fatalf("zero cache limit retained %d events", got)
	}
	if got := uncached.snapshot().counts[diagnosticPlayerAuthInput]; got != 1 {
		t.Fatalf("zero cache limit should preserve counters, got %d", got)
	}
}

func TestDiagnosticsTrackerRejectsInvalidCacheLimits(t *testing.T) {
	for _, limit := range []int{-1, maxScannerCacheLimit + 1} {
		if tracker, err := newDiagnosticsTracker(true, limit); err == nil || tracker != nil {
			t.Fatalf("cache limit %d should fail, tracker=%v err=%v", limit, tracker, err)
		}
	}
}

func TestDisabledDiagnosticsDoNotCollect(t *testing.T) {
	tracker, err := newDiagnosticsTracker(false, 4)
	if err != nil {
		t.Fatal(err)
	}
	tracker.observe("upstream->client", &packet.LevelChunk{})
	if snapshot := tracker.snapshot(); len(snapshot.events) != 0 || snapshot.counts[diagnosticLevelChunk] != 0 {
		t.Fatalf("disabled tracker collected packet metadata: %#v", snapshot)
	}
	if got := strings.Join(tracker.statusLines(), "\n"); !strings.Contains(got, "disabled") {
		t.Fatalf("disabled tracker status should explain state: %q", got)
	}
}
