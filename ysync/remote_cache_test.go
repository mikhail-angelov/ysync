package ysync

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteTreeCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-cache.json")
	cfg := Config{BaseURL: "https://webdav.yandex.ru", RemoteRoot: "/"}
	scannedAt := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	entries := map[string]RemoteEntry{
		"docs/a.txt": {Path: "docs/a.txt", Size: 12, ETag: "abc"},
	}

	if err := SaveRemoteTreeCache(path, cfg, scannedAt, entries); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadRemoteTreeCache(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("cache was not loaded")
	}
	if !got.ScannedAt.Equal(scannedAt) {
		t.Fatalf("ScannedAt = %s, want %s", got.ScannedAt, scannedAt)
	}
	if got.Entries["docs/a.txt"].ETag != "abc" {
		t.Fatalf("entry ETag = %q, want abc", got.Entries["docs/a.txt"].ETag)
	}
}

func TestRemoteTreeCacheIsBoundToRemoteRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-cache.json")
	cfg := Config{BaseURL: "https://webdav.yandex.ru", RemoteRoot: "/YSync"}
	if err := SaveRemoteTreeCache(path, cfg, time.Now(), map[string]RemoteEntry{}); err != nil {
		t.Fatal(err)
	}
	_, ok, err := LoadRemoteTreeCache(path, Config{BaseURL: cfg.BaseURL, RemoteRoot: "/Other"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("cache loaded for a different remote root")
	}
}
