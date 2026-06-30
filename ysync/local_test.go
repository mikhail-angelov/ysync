package ysync

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestScanLocalNormalizesUnicodePathKeys(t *testing.T) {
	root := t.TempDir()
	decomposed := filepath.Join(root, "Список АКР текстовыи\u0306.pdf")
	if err := os.WriteFile(decomposed, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignores, err := NewIgnoreMatcher("", true)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ScanLocal(root, ignores, Logger{out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	want := "Список АКР текстовый.pdf"
	if _, ok := entries[want]; !ok {
		t.Fatalf("normalized path %q not found in %#v", want, entries)
	}
}
