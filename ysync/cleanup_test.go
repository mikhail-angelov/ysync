package ysync

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupLocalTempsRemovesStaleTmpAndPartFiles(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "a.ysync.tmp")
	fresh := filepath.Join(root, "b.ysync.part")
	downloading := filepath.Join(root, "c.ysync-downloading")
	for _, path := range []string{stale, fresh, downloading} {
		if err := os.WriteFile(path, []byte("temp"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	plan, err := CleanupLocalTemps(root, 24*time.Hour, false, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Path != "a.ysync.tmp" {
		t.Fatalf("cleanup plan = %#v", plan)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp still exists or unexpected error: %v", err)
	}
	for _, path := range []string{fresh, downloading} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain: %v", path, err)
		}
	}
}

func TestCleanupRemoteTempsMovesStaleUploadTempToTrash(t *testing.T) {
	var destination string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "MOVE" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		destination = req.Header.Get("Destination")
		if got := req.Header.Get("Overwrite"); got != "F" {
			t.Fatalf("Overwrite = %q, want F", got)
		}
		return textResponse(http.StatusCreated, "201 Created", ""), nil
	})}
	client, err := NewWebDAVClient("https://example.test", "/YSync", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	remotes := map[string]RemoteEntry{
		"old.txt.ysync-uploading": {Path: "old.txt.ysync-uploading", ModTime: time.Now().Add(-48 * time.Hour)},
		"new.txt.ysync-uploading": {Path: "new.txt.ysync-uploading", ModTime: time.Now()},
		"normal.txt":              {Path: "normal.txt", ModTime: time.Now().Add(-48 * time.Hour)},
	}
	plan, mutated := CleanupRemoteTemps(context.Background(), client, "/YSync", remotes, 24*time.Hour, false, Logger{out: io.Discard})
	if !mutated {
		t.Fatal("expected remote cleanup mutation")
	}
	if len(plan) != 1 || plan[0].Path != "old.txt.ysync-uploading" {
		t.Fatalf("cleanup plan = %#v", plan)
	}
	if !strings.HasPrefix(destination, "https://example.test/ysync-trash/temp/") {
		t.Fatalf("Destination = %q, want /ysync-trash/temp", destination)
	}
}
