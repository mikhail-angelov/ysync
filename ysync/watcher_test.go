package ysync

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestEmitChangedLocalPathsSeparatesChangesAndDeletes(t *testing.T) {
	prev := map[string]LocalEntry{
		"a.txt": {Path: "a.txt", Size: 1, ModTime: at(1)},
		"b.txt": {Path: "b.txt", Size: 1, ModTime: at(1)},
	}
	current := map[string]LocalEntry{
		"a.txt": {Path: "a.txt", Size: 2, ModTime: at(2)},
		"c.txt": {Path: "c.txt", Size: 1, ModTime: at(1)},
	}
	var changed []string
	var deleted []string
	emitChangedLocalPaths(prev, current, func(path string) {
		changed = append(changed, path)
	}, func(path string) {
		deleted = append(deleted, path)
	})
	slices.Sort(changed)
	slices.Sort(deleted)
	if !slices.Equal(changed, []string{"a.txt", "c.txt"}) {
		t.Fatalf("changed = %#v", changed)
	}
	if !slices.Equal(deleted, []string{"b.txt"}) {
		t.Fatalf("deleted = %#v", deleted)
	}
}

func TestIgnoredLocalPathFiltersBeforeDebounce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".DS_Store")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignores, err := NewIgnoreMatcher("", false)
	if err != nil {
		t.Fatal(err)
	}
	if !ignoredLocalPath(path, ".DS_Store", ignores, Logger{out: io.Discard}) {
		t.Fatal("expected .DS_Store to be ignored")
	}
}

func TestIsFileLimitError(t *testing.T) {
	if !isFileLimitError(syscall.EMFILE) {
		t.Fatal("expected EMFILE to be detected")
	}
	if !isFileLimitError(syscall.ENFILE) {
		t.Fatal("expected ENFILE to be detected")
	}
	if !isFileLimitError(errors.New("too many open files")) {
		t.Fatal("expected string fallback to be detected")
	}
}

func TestLocalWatcherEmitsNestedFileChanges(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "dir")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	ignores, err := NewIgnoreMatcher("", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 4)
	watcher, err := StartLocalWatcher(ctx, root, ignores, 20*time.Millisecond, time.Hour, Logger{out: io.Discard}, func(path string) {
		ready <- path
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if watcher.Mode() != WatcherModeFSNotify {
		t.Fatalf("watcher mode = %s", watcher.Mode())
	}
	if err := os.WriteFile(filepath.Join(nested, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-ready:
			if got == "dir/a.txt" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for nested file event")
		}
	}
}
