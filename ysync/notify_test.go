package ysync

import (
	"errors"
	"io"
	"testing"
)

func TestNotifyConflictIsBestEffort(t *testing.T) {
	old := runNotification
	defer func() { runNotification = old }()
	called := false
	runNotification = func(title, message string) error {
		called = true
		if title != "ysync conflict" {
			t.Fatalf("title = %q", title)
		}
		return errors.New("notification failed")
	}
	NotifyConflict("a.txt", Logger{out: io.Discard, verbose: true})
	if !called {
		t.Fatal("notification was not attempted")
	}
}

func TestEscapeAppleScript(t *testing.T) {
	got := escapeAppleScript(`a "quote" and \ slash`)
	want := `a \"quote\" and \\ slash`
	if got != want {
		t.Fatalf("escape = %q, want %q", got, want)
	}
}
