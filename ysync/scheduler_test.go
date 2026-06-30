package ysync

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestSchedulerEnforcesConcurrencyLimit(t *testing.T) {
	s := NewScheduler(2, Logger{out: io.Discard})
	defer func() {
		_ = s.Shutdown(context.Background())
	}()

	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	active := 0
	maxActive := 0
	for _, path := range []string{"a", "b", "c", "d"} {
		path := path
		if !s.Schedule(Job{Kind: JobKindSync, Path: path, Run: func(context.Context) error {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			started <- struct{}{}
			<-release
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		}}) {
			t.Fatalf("failed to schedule %s", path)
		}
	}

	<-started
	<-started
	select {
	case <-started:
		t.Fatal("third job started before concurrency slot was available")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if maxActive > 2 {
		t.Fatalf("max active = %d, want <= 2", maxActive)
	}
}

func TestSchedulerDeduplicatesPendingAndActiveKeys(t *testing.T) {
	s := NewScheduler(1, Logger{out: io.Discard})
	defer func() {
		_ = s.Shutdown(context.Background())
	}()
	release := make(chan struct{})
	if !s.Schedule(Job{Kind: JobKindSync, Path: "a.txt", Run: func(context.Context) error {
		<-release
		return nil
	}}) {
		t.Fatal("expected first schedule to succeed")
	}
	if s.Schedule(Job{Kind: JobKindSync, Path: "a.txt", Run: func(context.Context) error { return nil }}) {
		t.Fatal("expected duplicate path to be rejected")
	}
	close(release)
}

func TestSchedulerCleanupKeysConflictWithSyncJobs(t *testing.T) {
	s := NewScheduler(2, Logger{out: io.Discard})
	defer func() {
		_ = s.Shutdown(context.Background())
	}()
	release := make(chan struct{})
	if !s.Schedule(Job{Kind: JobKindSync, Path: "a.txt", Run: func(context.Context) error {
		<-release
		return nil
	}}) {
		t.Fatal("expected sync job to schedule")
	}
	if s.Schedule(Job{Kind: JobKindCleanup, Path: "a.txt.ysync-uploading", Keys: []string{"a.txt", "a.txt.ysync-uploading"}, Run: func(context.Context) error { return nil }}) {
		t.Fatal("expected cleanup job to conflict with active sync key")
	}
	close(release)
}

func TestSchedulerShutdownRejectsNewJobsAndLetsRunningFinish(t *testing.T) {
	s := NewScheduler(1, Logger{out: io.Discard})
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	if !s.Schedule(Job{Kind: JobKindSync, Path: "a.txt", Run: func(context.Context) error {
		close(started)
		<-release
		close(finished)
		return nil
	}}) {
		t.Fatal("expected schedule to succeed")
	}
	<-started
	s.StopScheduling()
	if s.Schedule(Job{Kind: JobKindSync, Path: "b.txt", Run: func(context.Context) error { return nil }}) {
		t.Fatal("expected schedule after shutdown to be rejected")
	}
	select {
	case <-finished:
		t.Fatal("running job finished before release")
	case <-time.After(50 * time.Millisecond):
	}
	done := make(chan error, 1)
	go func() {
		done <- s.Shutdown(context.Background())
	}()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
