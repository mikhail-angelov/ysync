package ysync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Logger struct {
	out     io.Writer
	verbose bool
}

func NewLogger(verbose bool) Logger {
	return Logger{out: os.Stdout, verbose: verbose}
}

func NewFileLogger(path string, verbose bool) (Logger, io.Closer, error) {
	if path == "" {
		return NewLogger(verbose), nil, nil
	}
	path = expandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Logger{}, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Logger{}, nil, err
	}
	return Logger{out: f, verbose: verbose}, f, nil
}

func (l Logger) Debug(msg string, kv ...any) {
	if l.verbose {
		l.log("DEBUG", msg, kv...)
	}
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (l Logger) Info(msg string, kv ...any) {
	l.log("INFO", msg, kv...)
}

func (l Logger) Warn(msg string, kv ...any) {
	l.log("WARN", msg, kv...)
}

func (l Logger) Error(msg string, kv ...any) {
	l.log("ERROR", msg, kv...)
}

func (l Logger) log(level, msg string, kv ...any) {
	if l.out == nil {
		l.out = os.Stdout
	}
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString(" [")
	b.WriteString(level)
	b.WriteString("] ")
	b.WriteString(msg)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		b.WriteString(fmt.Sprint(kv[i+1]))
	}
	fmt.Fprintln(l.out, b.String())
}
