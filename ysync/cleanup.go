package ysync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func CleanupLocalTemps(localRoot string, ttl time.Duration, dryRun bool, log Logger) ([]PlanItem, error) {
	if ttl <= 0 {
		return nil, nil
	}
	var plan []PlanItem
	cutoff := time.Now().Add(-ttl)
	err := filepath.WalkDir(localRoot, func(full string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(localRoot, full)
		if err != nil {
			return err
		}
		rel = cleanRel(rel)
		if !isStaleLocalTempCandidate(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !normalizeTime(info.ModTime()).Before(normalizeTime(cutoff)) {
			return nil
		}
		plan = append(plan, PlanItem{Action: ActionCleanup, Path: rel, Reason: "stale local temp, remove"})
		if dryRun {
			return nil
		}
		if err := os.Remove(full); err != nil {
			return err
		}
		log.Debug("removed stale local temp", "path", rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cleanup local temps: %w", err)
	}
	return plan, nil
}

func CleanupRemoteTemps(ctx context.Context, client *WebDAVClient, remoteRoot string, remotes map[string]RemoteEntry, ttl time.Duration, dryRun bool, log Logger) ([]PlanItem, bool) {
	if ttl <= 0 {
		return nil, false
	}
	if err := ensureRemoteTrashOutsideRoot(remoteRoot); err != nil {
		log.Warn("skipping remote temp cleanup", "error", err)
		return nil, false
	}
	cutoff := time.Now().Add(-ttl)
	var plan []PlanItem
	mutated := false
	for path, entry := range remotes {
		if !isServiceRemoteTemp(path, entry.IsDir) {
			continue
		}
		if entry.ModTime.IsZero() || !entry.ModTime.Before(normalizeTime(cutoff)) {
			continue
		}
		plan = append(plan, PlanItem{Action: ActionCleanup, Path: path, Reason: "stale remote temp, move to trash"})
		if dryRun {
			continue
		}
		if err := expectWebDAVStatus(client.MoveStaleTempToTrash(ctx, path, remoteTempTrashPath(path))); err != nil {
			log.Warn("failed remote temp cleanup", "path", path, "error", err)
			continue
		}
		mutated = true
		log.Debug("moved stale remote temp to trash", "path", path)
	}
	return plan, mutated
}

func isStaleLocalTempCandidate(rel string) bool {
	return matchIgnorePattern("*.ysync.tmp", rel, filepath.Base(rel), false) ||
		matchIgnorePattern("*.ysync.part", rel, filepath.Base(rel), false) ||
		matchIgnorePattern("*.ysync-downloading", rel, filepath.Base(rel), false)
}

func remoteTempTrashPath(relPath string) string {
	return "/ysync-trash/temp/" + time.Now().UTC().Format("20060102T150405Z") + "/" + cleanRel(relPath)
}
