package ysync

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadStateAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := SyncState{
		Version:    1,
		LocalRoot:  "/tmp/local",
		RemoteRoot: "/",
		Entries: map[string]SyncEntry{
			"a.txt": {Path: "a.txt", LocalSize: 1, RemoteSize: 1, Status: StatusSynced},
		},
	}
	if err := SaveStateAtomic(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Entries["a.txt"].Status != StatusSynced {
		t.Fatalf("loaded status = %q", loaded.Entries["a.txt"].Status)
	}
}
