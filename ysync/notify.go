package ysync

import (
	"os/exec"
	"runtime"
)

var runNotification = func(title, message string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return exec.Command("osascript", "-e", `display notification "`+escapeAppleScript(message)+`" with title "`+escapeAppleScript(title)+`"`).Run()
}

func NotifyConflict(path string, log Logger) {
	if err := runNotification("ysync conflict", "File changed both locally and in Yandex Disk: "+path); err != nil {
		log.Debug("notification failed", "path", path, "error", err)
	}
}

func escapeAppleScript(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
