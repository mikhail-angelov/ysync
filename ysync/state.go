package ysync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func DefaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ysync", "state.json"), nil
}

func LoadState(path string) (SyncState, error) {
	if path == "" {
		var err error
		path, err = DefaultStatePath()
		if err != nil {
			return SyncState{}, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return SyncState{Version: 1, Entries: map[string]SyncEntry{}}, nil
	}
	if err != nil {
		return SyncState{}, err
	}
	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return SyncState{}, err
	}
	if state.Entries == nil {
		state.Entries = map[string]SyncEntry{}
	}
	return state, nil
}

func SaveStateAtomic(path string, state SyncState) error {
	if path == "" {
		var err error
		path, err = DefaultStatePath()
		if err != nil {
			return err
		}
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.LastSyncedAt.IsZero() {
		state.LastSyncedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func EntryFromMetadata(path string, local LocalEntry, remote RemoteEntry, status string) SyncEntry {
	now := time.Now().UTC()
	return SyncEntry{
		Path:          path,
		IsDir:         local.IsDir || remote.IsDir,
		LocalModTime:  normalizeTime(local.ModTime),
		LocalSize:     local.Size,
		RemoteModTime: normalizeTime(remote.ModTime),
		RemoteSize:    remote.Size,
		RemoteETag:    remote.ETag,
		Status:        status,
		LastSyncedAt:  now,
	}
}

func EnsureStateRoots(state SyncState, localRoot, remoteRoot string) error {
	if state.LocalRoot != "" && state.LocalRoot != localRoot {
		return fmt.Errorf("state local root %q does not match %q", state.LocalRoot, localRoot)
	}
	if state.RemoteRoot != "" && state.RemoteRoot != remoteRoot {
		return fmt.Errorf("state remote root %q does not match %q", state.RemoteRoot, remoteRoot)
	}
	return nil
}
