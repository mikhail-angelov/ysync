package ysync

import "testing"

func TestDefaultIgnorePatterns(t *testing.T) {
	matcher, err := NewIgnoreMatcher("", true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		".DS_Store",
		"._photo.jpg",
		"docs/a.txt.ysync-uploading",
		"docs/a.txt.ysync-downloading",
		"docs/a.txt.conflict-local-20260628T120000Z",
		"docs/a.txt.conflict-remote-20260628T120000Z",
	}
	for _, path := range cases {
		if !matcher.Match(path, false) {
			t.Fatalf("expected %q to be ignored", path)
		}
	}
	if !matcher.Match("node_modules", true) {
		t.Fatal("expected node_modules directory to be ignored")
	}
	if matcher.Match("docs/real.txt", false) {
		t.Fatal("did not expect normal file to be ignored")
	}
}

func TestCleanRelNormalizesUnicodeToNFC(t *testing.T) {
	got := cleanRel("Загрузки/Список АКР текстовыи\u0306.pdf")
	want := "Загрузки/Список АКР текстовый.pdf"
	if got != want {
		t.Fatalf("cleanRel = %q, want %q", got, want)
	}
}
