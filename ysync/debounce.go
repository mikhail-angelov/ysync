package ysync

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

type StableDebouncer struct {
	root    string
	delay   time.Duration
	log     Logger
	onReady func(string)

	mu      sync.Mutex
	pending map[string]chan struct{}
}

func NewStableDebouncer(root string, delay time.Duration, log Logger, onReady func(string)) *StableDebouncer {
	if delay <= 0 {
		delay = time.Second
	}
	return &StableDebouncer{
		root:    filepath.Clean(root),
		delay:   delay,
		log:     log,
		onReady: onReady,
		pending: map[string]chan struct{}{},
	}
}

func (d *StableDebouncer) Notify(relPath string) {
	relPath = cleanRel(relPath)
	if relPath == "" {
		return
	}
	d.log.Info("debounce scheduled", "path", relPath, "delay", d.delay)
	done := make(chan struct{})

	d.mu.Lock()
	if old, ok := d.pending[relPath]; ok {
		close(old)
		d.log.Info("debounce rescheduled", "path", relPath)
	}
	d.pending[relPath] = done
	d.mu.Unlock()

	go d.waitStable(relPath, done)
}

func (d *StableDebouncer) EmitNow(relPath string) {
	relPath = cleanRel(relPath)
	if relPath == "" {
		return
	}
	d.mu.Lock()
	if old, ok := d.pending[relPath]; ok {
		close(old)
		delete(d.pending, relPath)
	}
	d.mu.Unlock()
	d.log.Info("debounce emit now", "path", relPath)
	d.onReady(relPath)
}

func (d *StableDebouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for path, done := range d.pending {
		close(done)
		delete(d.pending, path)
	}
}

func (d *StableDebouncer) waitStable(relPath string, done <-chan struct{}) {
	for {
		first, ok := d.snapshot(relPath)
		if !ok {
			if !d.wait(done) {
				return
			}
			continue
		}
		if !d.wait(done) {
			return
		}
		second, ok := d.snapshot(relPath)
		if !ok || !sameFileSnapshot(first, second) {
			continue
		}

		d.mu.Lock()
		if current, ok := d.pending[relPath]; ok && current == done {
			delete(d.pending, relPath)
			d.mu.Unlock()
			d.log.Info("debounce ready", "path", relPath)
			d.onReady(relPath)
			return
		}
		d.mu.Unlock()
		return
	}
}

func (d *StableDebouncer) wait(done <-chan struct{}) bool {
	timer := time.NewTimer(d.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-done:
		return false
	}
}

func (d *StableDebouncer) snapshot(relPath string) (fileSnapshot, bool) {
	info, err := os.Stat(filepath.Join(d.root, filepath.FromSlash(relPath)))
	if err != nil {
		d.log.Debug("stable check pending", "path", relPath, "error", err)
		return fileSnapshot{}, false
	}
	return fileSnapshot{
		size:    fileSize(info),
		modTime: normalizeTime(info.ModTime()),
	}, true
}

type fileSnapshot struct {
	size    int64
	modTime time.Time
}

func sameFileSnapshot(a, b fileSnapshot) bool {
	return a.size == b.size && a.modTime.Equal(b.modTime)
}
