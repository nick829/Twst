package main

import (
	"fmt"
	"sync"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const maxScannerCacheLimit = 4096

type diagnosticKind uint8

const (
	diagnosticLevelChunk diagnosticKind = iota
	diagnosticUpdateBlock
	diagnosticPlayerAuthInput
	diagnosticMovePlayer
	diagnosticKindCount
)

func (kind diagnosticKind) String() string {
	switch kind {
	case diagnosticLevelChunk:
		return "LevelChunk"
	case diagnosticUpdateBlock:
		return "UpdateBlock"
	case diagnosticPlayerAuthInput:
		return "PlayerAuthInput"
	case diagnosticMovePlayer:
		return "MovePlayer"
	default:
		return "unknown"
	}
}

type diagnosticEvent struct {
	kind          diagnosticKind
	direction     string
	subChunkCount uint32
	payloadBytes  int
	cacheEnabled  bool
}

type diagnosticsTracker struct {
	enabled    bool
	cacheLimit int

	mu     sync.Mutex
	counts [diagnosticKindCount]uint64
	cache  []diagnosticEvent
	next   int
	cached int
}

type diagnosticsSnapshot struct {
	enabled    bool
	cacheLimit int
	counts     [diagnosticKindCount]uint64
	events     []diagnosticEvent
}

func newDiagnosticsTracker(enabled bool, cacheLimit int) (*diagnosticsTracker, error) {
	if cacheLimit < 0 || cacheLimit > maxScannerCacheLimit {
		return nil, fmt.Errorf("SCANNER_CACHE_LIMIT must be between 0 and %d", maxScannerCacheLimit)
	}
	return &diagnosticsTracker{
		enabled:    enabled,
		cacheLimit: cacheLimit,
		cache:      make([]diagnosticEvent, cacheLimit),
	}, nil
}

// observe records only packet types and bounded metadata seen on this connection.
// It deliberately does not retain positions, block identifiers, or chunk payloads.
func (tracker *diagnosticsTracker) observe(direction string, pk any) {
	if !tracker.enabled {
		return
	}
	event := diagnosticEvent{direction: direction}
	switch value := pk.(type) {
	case *packet.LevelChunk:
		event.kind = diagnosticLevelChunk
		event.subChunkCount = value.SubChunkCount
		event.payloadBytes = len(value.RawPayload)
		event.cacheEnabled = value.CacheEnabled
	case *packet.UpdateBlock:
		event.kind = diagnosticUpdateBlock
	case *packet.PlayerAuthInput:
		event.kind = diagnosticPlayerAuthInput
	case *packet.MovePlayer:
		event.kind = diagnosticMovePlayer
	default:
		return
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.counts[event.kind]++
	if len(tracker.cache) == 0 {
		return
	}
	tracker.cache[tracker.next] = event
	tracker.next = (tracker.next + 1) % len(tracker.cache)
	if tracker.cached < len(tracker.cache) {
		tracker.cached++
	}
}

func (tracker *diagnosticsTracker) reset() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.counts = [diagnosticKindCount]uint64{}
	for i := range tracker.cache {
		tracker.cache[i] = diagnosticEvent{}
	}
	tracker.next = 0
	tracker.cached = 0
}

func (tracker *diagnosticsTracker) snapshot() diagnosticsSnapshot {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	snapshot := diagnosticsSnapshot{
		enabled:    tracker.enabled,
		cacheLimit: tracker.cacheLimit,
		counts:     tracker.counts,
		events:     make([]diagnosticEvent, tracker.cached),
	}
	if tracker.cached == 0 {
		return snapshot
	}
	start := (tracker.next - tracker.cached + len(tracker.cache)) % len(tracker.cache)
	for i := range snapshot.events {
		snapshot.events[i] = tracker.cache[(start+i)%len(tracker.cache)]
	}
	return snapshot
}

func (tracker *diagnosticsTracker) statusLines() []string {
	snapshot := tracker.snapshot()
	if !snapshot.enabled {
		return []string{
			"Scanner diagnostics are disabled (SCANNER_ENABLED=false).",
			"No packet metadata is being collected.",
		}
	}
	lines := []string{
		"Scanner diagnostics: enabled; packet metadata only.",
		fmt.Sprintf("Observed: LevelChunk %d, UpdateBlock %d.", snapshot.counts[diagnosticLevelChunk], snapshot.counts[diagnosticUpdateBlock]),
		fmt.Sprintf("Movement packets: PlayerAuthInput %d, MovePlayer %d.", snapshot.counts[diagnosticPlayerAuthInput], snapshot.counts[diagnosticMovePlayer]),
		fmt.Sprintf("Recent metadata cache: %d/%d entries.", len(snapshot.events), snapshot.cacheLimit),
		"Chunk payloads are not decoded; unseen blocks or server data are not inspected.",
	}
	if len(snapshot.events) == 0 {
		return append(lines, "No supported packets observed yet.")
	}
	lines = append(lines, "Recent observed packets (oldest first):")
	for _, event := range snapshot.events {
		switch event.kind {
		case diagnosticLevelChunk:
			cacheState := "off"
			if event.cacheEnabled {
				cacheState = "on"
			}
			lines = append(lines, fmt.Sprintf("%s LevelChunk: sub-chunks=%d, payload-bytes=%d, blob-cache=%s.",
				event.direction, event.subChunkCount, event.payloadBytes, cacheState))
		default:
			lines = append(lines, fmt.Sprintf("%s %s.", event.direction, event.kind))
		}
	}
	return lines
}
