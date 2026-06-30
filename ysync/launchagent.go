package ysync

import (
	"fmt"
	"path/filepath"
	"strings"
)

func GenerateLaunchAgentPlist(cfg Config, executable string) string {
	if executable == "" {
		executable = "/usr/local/bin/ysync"
	}
	stdout := expandHome("~/Library/Logs/ysync/stdout.log")
	stderr := expandHome("~/Library/Logs/ysync/stderr.log")
	args := []string{
		executable,
		"-local", cfg.LocalRoot,
		"-remote", cfg.RemoteRoot,
		"-url", cfg.BaseURL,
		"-conflict", cfg.ConflictStrategy,
		"-delete-mode", cfg.DeleteMode,
	}
	if cfg.KeychainProfile != "" {
		args = append(args, "-keychain-profile", cfg.KeychainProfile)
	}
	if cfg.LogFile != "" {
		args = append(args, "-log-file", cfg.LogFile)
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	writePlistKeyString(&b, "Label", "com.ysync.service")
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range args {
		b.WriteString("    <string>")
		b.WriteString(xmlEscape(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n")
	b.WriteString("  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n")
	b.WriteString("  <dict>\n")
	b.WriteString("    <key>SuccessfulExit</key>\n")
	b.WriteString("    <false/>\n")
	b.WriteString("  </dict>\n")
	b.WriteString("  <key>ThrottleInterval</key>\n")
	b.WriteString("  <integer>10</integer>\n")
	writePlistKeyString(&b, "StandardOutPath", filepath.Clean(stdout))
	writePlistKeyString(&b, "StandardErrorPath", filepath.Clean(stderr))
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func writePlistKeyString(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "  <key>%s</key>\n", xmlEscape(key))
	fmt.Fprintf(b, "  <string>%s</string>\n", xmlEscape(value))
}

func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}
