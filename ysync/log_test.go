package ysync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFileLoggerWritesStructuredLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ysync.log")
	log, closer, err := NewFileLogger(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	log.Info("upload completed", "path", "a.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "[INFO] upload completed path=a.txt") {
		t.Fatalf("log output = %q", got)
	}
}
