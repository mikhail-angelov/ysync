package ysync

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var defaultIgnorePatterns = []string{
	".DS_Store",
	"Thumbs.db",
	"Desktop.ini",
	".localized",
	"._*",
	".Trashes",
	".git/",
	".svn/",
	".hg/",
	"node_modules/",
	"vendor/",
	"*.ysync.tmp",
	"*.ysync.part",
	"*.ysync-downloading",
	"*.ysync-uploading",
	"*.conflict-local-*",
	"*.conflict-remote-*",
	"*.ysync-conflict-*",
}

type IgnoreMatcher struct {
	patterns []string
}

func NewIgnoreMatcher(ignoreFile string, includeHidden bool) (IgnoreMatcher, error) {
	patterns := append([]string{}, defaultIgnorePatterns...)
	if !includeHidden {
		patterns = append(patterns, ".*")
	}
	if ignoreFile != "" {
		extra, err := readIgnoreFile(ignoreFile)
		if err != nil {
			return IgnoreMatcher{}, err
		}
		patterns = append(patterns, extra...)
	}
	return IgnoreMatcher{patterns: patterns}, nil
}

func readIgnoreFile(name string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, filepath.ToSlash(line))
	}
	return patterns, scanner.Err()
}

func (m IgnoreMatcher) Match(rel string, isDir bool) bool {
	rel = cleanRel(rel)
	base := path.Base(rel)
	for _, pattern := range m.patterns {
		if matchIgnorePattern(pattern, rel, base, isDir) {
			return true
		}
	}
	return false
}

func matchIgnorePattern(pattern, rel, base string, isDir bool) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		dirPattern := strings.TrimSuffix(pattern, "/")
		if !isDir && !strings.Contains(rel, "/") {
			return false
		}
		return rel == dirPattern || strings.HasPrefix(rel, dirPattern+"/") || base == dirPattern
	}
	targets := []string{base}
	if strings.Contains(pattern, "/") {
		targets = append(targets, rel)
	}
	for _, target := range targets {
		ok, err := path.Match(pattern, target)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func cleanRel(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." {
		return ""
	}
	return norm.NFC.String(strings.TrimPrefix(p, "/"))
}
