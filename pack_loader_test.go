package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPack(t *testing.T, dir, filename, packName, uuidValue string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create pack file: %v", err)
	}
	zw := zip.NewWriter(f)
	manifest := `{
		"format_version": 2,
		"header": {
			"name": "` + packName + `",
			"description": "test pack",
			"uuid": "` + uuidValue + `",
			"version": [1, 0, 0],
			"min_engine_version": [1, 20, 0]
		},
		"modules": [
			{
				"uuid": "` + uuidValue + `-module",
				"description": "test module",
				"type": "resources",
				"version": [1, 0, 0]
			}
		]
	}`
	w, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	if _, err := w.Write([]byte(manifest)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

func TestDiscoverPacksEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if got := discoverPacks(dir); len(got) != 0 {
		t.Fatalf("expected no packs, got %d", len(got))
	}
}

func TestDiscoverPacksLoadsValidAndSkipsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeTestPack(t, dir, "pack-one.mcpack", "Pack One", "11111111-1111-1111-1111-111111111111")
	writeTestPack(t, dir, "pack-two.ZIP", "Pack Two", "22222222-2222-2222-2222-222222222222")
	if err := os.WriteFile(filepath.Join(dir, "bad.mcpack"), []byte("not a zip archive"), 0o600); err != nil {
		t.Fatalf("write invalid pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	packs := discoverPacks(dir)
	if got := len(packs); got != 2 {
		t.Fatalf("expected 2 valid packs, got %d", got)
	}
	if packs[0].Name() == "" || packs[1].Name() == "" {
		t.Fatal("expected names to be populated")
	}
}
