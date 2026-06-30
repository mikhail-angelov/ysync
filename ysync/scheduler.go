package ysync

import (
	"context"
	"errors"
	"sync"
)

type JobKind string

const (
	JobKindSync    JobKind = "sync"
	JobKindCleanup JobKind = "cleanup"
)

type Job struct {
	Kind JobKind
	Path string
	Keys []string
	Run  func(context.Context) error
}

type Scheduler struct {
	log Logger

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []Job
	locked  map[string]struct{}
	stopped bool
	running int

	wg sync.WaitGroup
}

func NewScheduler(concurrency int, log Logger) *Scheduler {
	if concurrency < 1 {
		concurrency = 1
	}
	s := &Scheduler{
		log:    log,
		locked: map[string]struct{}{},
	}
	s.cond = sync.NewCond(&s.mu)
	for i := 0; i < concurrency; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *Scheduler) Schedule(job Job) bool {
	keys := jobKeys(job)
	if len(keys) == 0 || job.Run == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	for _, key := range keys {
		if _, ok := s.locked[key]; ok {
			return false
		}
	}
	for _, key := range keys {
		s.locked[key] = struct{}{}
	}
	s.queue = append(s.queue, job)
	s.cond.Signal()
	return true
}

func (s *Scheduler) StopScheduling() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		for _, job := range s.queue {
			s.unlock(job)
		}
		s.queue = nil
		s.cond.Broadcast()
	}
}

func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.StopScheduling()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		job, ok := s.next()
		if !ok {
			return
		}
		if err := job.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("job failed", "kind", job.Kind, "path", job.Path, "error", err)
		}
		s.finish(job)
	}
}

func (s *Scheduler) next() (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.stopped {
		s.cond.Wait()
	}
	if s.stopped {
		return Job{}, false
	}
	job := s.queue[0]
	s.queue = s.queue[1:]
	s.running++
	return job, true
}

func (s *Scheduler) finish(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	s.unlock(job)
	if s.stopped && s.running == 0 {
		s.cond.Broadcast()
	}
}

func (s *Scheduler) unlock(job Job) {
	for _, key := range jobKeys(job) {
		delete(s.locked, key)
	}
}

func jobKeys(job Job) []string {
	if len(job.Keys) > 0 {
		keys := make([]string, 0, len(job.Keys))
		seen := map[string]struct{}{}
		for _, key := range job.Keys {
			key = cleanRel(key)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
		return keys
	}
	if cleanRel(job.Path) == "" {
		return nil
	}
	return []string{cleanRel(job.Path)}
}
