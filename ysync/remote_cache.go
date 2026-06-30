package ysync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type RemoteTreeCache struct {
	Version    int                    `json:"version"`
	BaseURL    string                 `json:"base_url"`
	RemoteRoot string                 `json:"remote_root"`
	ScannedAt  time.Time              `json:"scanned_at"`
	Entries    map[string]RemoteEntry `json:"entries"`
}

func DefaultRemoteCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ysync", "remote-cache.json"), nil
}

func LoadRemoteTreeCache(path string, cfg Config) (RemoteTreeCache, bool, error) {
	if path == "" {
		var err error
		path, err = DefaultRemoteCachePath()
		if err != nil {
			return RemoteTreeCache{}, false, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RemoteTreeCache{}, false, nil
	}
	if err != nil {
		return RemoteTreeCache{}, false, err
	}
	var cache RemoteTreeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return RemoteTreeCache{}, false, err
	}
	if cache.BaseURL != cfg.BaseURL || cache.RemoteRoot != cfg.RemoteRoot {
		return RemoteTreeCache{}, false, nil
	}
	if cache.Entries == nil {
		cache.Entries = map[string]RemoteEntry{}
	}
	return cache, true, nil
}

func SaveRemoteTreeCache(path string, cfg Config, scannedAt time.Time, entries map[string]RemoteEntry) error {
	if path == "" {
		var err error
		path, err = DefaultRemoteCachePath()
		if err != nil {
			return err
		}
	}
	cache := RemoteTreeCache{
		Version:    1,
		BaseURL:    cfg.BaseURL,
		RemoteRoot: cfg.RemoteRoot,
		ScannedAt:  scannedAt.UTC(),
		Entries:    entries,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
