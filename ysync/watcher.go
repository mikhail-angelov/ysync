package ysync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

type WatcherMode string

const (
	WatcherModeFSNotify WatcherMode = "fsnotify"
	WatcherModePolling  WatcherMode = "polling"
)

type LocalWatcher struct {
	root     string
	ignores  IgnoreMatcher
	log      Logger
	watcher  *fsnotify.Watcher
	debounce *StableDebouncer
	mode     WatcherMode
}

func StartLocalWatcher(ctx context.Context, root string, ignores IgnoreMatcher, debounceDelay, pollInterval time.Duration, log Logger, onStable func(string)) (*LocalWatcher, error) {
	root = filepath.Clean(root)
	debouncer := NewStableDebouncer(root, debounceDelay, log, onStable)
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		if isFileLimitError(err) {
			logWatcherLimit(root, log, err)
			w := &LocalWatcher{root: root, ignores: ignores, log: log, debounce: debouncer, mode: WatcherModePolling}
			go w.poll(ctx, pollInterval)
			return w, nil
		}
		return nil, err
	}

	w := &LocalWatcher{
		root:     root,
		ignores:  ignores,
		log:      log,
		watcher:  fsWatcher,
		debounce: debouncer,
		mode:     WatcherModeFSNotify,
	}
	if err := w.watchRecursive(root); err != nil {
		if isFileLimitError(err) {
			logWatcherLimit(root, log, err)
			_ = fsWatcher.Close()
			w.watcher = nil
			w.mode = WatcherModePolling
			go w.poll(ctx, pollInterval)
			return w, nil
		}
		_ = fsWatcher.Close()
		return nil, err
	}
	go w.run(ctx, pollInterval)
	return w, nil
}

func (w *LocalWatcher) Mode() WatcherMode {
	return w.mode
}

func (w *LocalWatcher) Close() error {
	w.debounce.Stop()
	if w.watcher == nil {
		return nil
	}
	return w.watcher.Close()
}

func (w *LocalWatcher) run(ctx context.Context, pollInterval time.Duration) {
	defer w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(ctx, event, pollInterval)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if isFileLimitError(err) {
				logWatcherLimit(w.root, w.log, err)
				w.mode = WatcherModePolling
				go w.poll(ctx, pollInterval)
				return
			}
			w.log.Warn("watcher error", "error", err)
		}
	}
}

func (w *LocalWatcher) handleEvent(ctx context.Context, event fsnotify.Event, pollInterval time.Duration) {
	rel, ok := w.relFromFull(event.Name)
	if !ok || rel == "" {
		return
	}
	w.log.Info("fs event", "path", rel, "op", event.Op.String())
	if ignoredLocalPath(event.Name, rel, w.ignores, w.log) {
		w.log.Info("fs event ignored", "path", rel)
		return
	}
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.log.Info("watching new directory", "path", rel)
			if err := w.watchRecursive(event.Name); err != nil {
				if isFileLimitError(err) {
					logWatcherLimit(event.Name, w.log, err)
					w.mode = WatcherModePolling
					go w.poll(ctx, pollInterval)
				} else {
					w.log.Warn("failed to watch new directory", "path", rel, "error", err)
				}
			}
		}
	}
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		_ = w.watcher.Remove(event.Name)
		w.debounce.EmitNow(rel)
		return
	}
	w.debounce.Notify(rel)
}

func (w *LocalWatcher) watchRecursive(root string) error {
	return filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, ok := w.relFromFull(full)
		if ok && rel != "" && ignoredLocalPath(full, rel, w.ignores, w.log) {
			return filepath.SkipDir
		}
		if err := w.watcher.Add(full); err != nil {
			return fmt.Errorf("watch %s: %w", full, err)
		}
		return nil
	})
}

func (w *LocalWatcher) poll(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	prev := map[string]LocalEntry{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.log.Info("polling local tree")
			current, err := ScanLocal(w.root, w.ignores, w.log)
			if err != nil {
				w.log.Warn("polling local scan failed", "error", err)
				continue
			}
			emitChangedLocalPaths(prev, current, w.debounce.Notify, w.debounce.EmitNow)
			prev = current
		}
	}
}

func emitChangedLocalPaths(prev, current map[string]LocalEntry, emitChanged, emitDeleted func(string)) {
	for path, entry := range current {
		old, ok := prev[path]
		if !ok || localEntryChanged(old, entry) {
			emitChanged(path)
		}
	}
	for path := range prev {
		if _, ok := current[path]; !ok {
			emitDeleted(path)
		}
	}
}

func localEntryChanged(a, b LocalEntry) bool {
	return a.IsDir != b.IsDir || a.Size != b.Size || !a.ModTime.Equal(b.ModTime)
}

func (w *LocalWatcher) relFromFull(full string) (string, bool) {
	rel, err := filepath.Rel(w.root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return cleanRel(rel), true
}

func ignoredLocalPath(full, rel string, ignores IgnoreMatcher, log Logger) bool {
	info, err := os.Lstat(full)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		log.Warn("skipping symlink", "path", rel)
		return true
	}
	isDir := err == nil && info.IsDir()
	return ignores.Match(rel, isDir)
}

func isFileLimitError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "too many open files")
}

func logWatcherLimit(path string, log Logger, err error) {
	log.Error("watcher limit reached: too many open files", "path", path, "error", err)
	log.Warn("switching to degraded polling mode; increase maxfiles via launchctl limit maxfiles if recursive watching is required")
}
