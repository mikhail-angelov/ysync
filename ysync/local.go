package ysync

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func ScanLocal(root string, ignores IgnoreMatcher, log Logger) (map[string]LocalEntry, error) {
	entries := make(map[string]LocalEntry)
	root = filepath.Clean(root)
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == root {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = cleanRel(rel)
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			log.Warn("skipping symlink", "path", rel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ignores.Match(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err = os.Stat(full)
		if err != nil {
			return err
		}
		entries[rel] = LocalEntry{
			Path:    rel,
			IsDir:   info.IsDir(),
			ModTime: normalizeTime(info.ModTime()),
			Size:    fileSize(info),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan local: %w", err)
	}
	return entries, nil
}

func SortedLocalPaths(entries map[string]LocalEntry) []string {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func fileSize(info os.FileInfo) int64 {
	if info.IsDir() {
		return 0
	}
	return info.Size()
}
