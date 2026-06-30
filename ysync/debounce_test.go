package ysync

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStableDebouncerWaitsForSizeAndModTimeToSettle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	debouncer := NewStableDebouncer(root, 20*time.Millisecond, Logger{out: io.Discard}, func(path string) {
		ready <- path
	})
	defer debouncer.Stop()

	debouncer.Notify("a.txt")
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ready:
		if got != "a.txt" {
			t.Fatalf("ready path = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stable path")
	}
}

func TestStableDebouncerEmitNowBypassesMissingFileStability(t *testing.T) {
	ready := make(chan string, 1)
	debouncer := NewStableDebouncer(t.TempDir(), time.Hour, Logger{out: io.Discard}, func(path string) {
		ready <- path
	})
	defer debouncer.Stop()

	debouncer.Notify("deleted.txt")
	debouncer.EmitNow("deleted.txt")

	select {
	case got := <-ready:
		if got != "deleted.txt" {
			t.Fatalf("ready path = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for immediate path")
	}
}
