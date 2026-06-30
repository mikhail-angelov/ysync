package ysync

import (
	"flag"
	"fmt"
	"time"
)

type Config struct {
	LocalRoot         string
	RemoteRoot        string
	BaseURL           string
	User              string
	Pass              string
	Token             string
	KeychainProfile   string
	Interval          time.Duration
	Debounce          time.Duration
	LocalScanInterval time.Duration
	RemoteWorkers     int
	Concurrency       int
	ConflictStrategy  string
	Delete            bool
	DeleteMode        string
	ImportRemote      bool
	InitStrategy      string
	IgnoreFile        string
	IncludeHidden     bool
	DryRun            bool
	TempTTL           time.Duration
	RemoteTempTTL     time.Duration
	ShowCleanup       bool
	Bandwidth         int64
	Verbose           bool
	LogFile           string
	UIAddr            string
	RemoteCachePath   string
	ShowVersion       bool
	StatePath         string
	ShowLaunchAgent   bool
}

func ParseConfig(args []string) (Config, error) {
	cfg := Config{}
	fs := flag.NewFlagSet("ysync", flag.ContinueOnError)
	fs.StringVar(&cfg.LocalRoot, "local", "", "Local folder to sync")
	fs.StringVar(&cfg.RemoteRoot, "remote", "/", "Remote Yandex Disk folder")
	fs.StringVar(&cfg.BaseURL, "url", "https://webdav.yandex.ru", "WebDAV base URL")
	fs.StringVar(&cfg.User, "user", "", "Yandex login")
	fs.StringVar(&cfg.Pass, "pass", "", "Yandex app password")
	fs.StringVar(&cfg.Token, "token", "", "Yandex OAuth token used as Basic Auth password with -user")
	fs.StringVar(&cfg.KeychainProfile, "keychain-profile", "", "Read credentials from macOS Keychain")
	fs.DurationVar(&cfg.Interval, "interval", 5*time.Minute, "Remote scan/cache TTL")
	fs.DurationVar(&cfg.Debounce, "debounce", 3*time.Second, "File debounce delay")
	fs.DurationVar(&cfg.LocalScanInterval, "local-scan-interval", time.Minute, "Fallback local polling interval")
	fs.IntVar(&cfg.RemoteWorkers, "remote-workers", 1, "Remote crawl workers")
	fs.IntVar(&cfg.Concurrency, "concurrency", 3, "Max concurrent operations")
	fs.StringVar(&cfg.ConflictStrategy, "conflict", "skip", "Conflict strategy: skip/local/remote")
	fs.BoolVar(&cfg.Delete, "delete", true, "Enable delete sync for tracked files")
	fs.StringVar(&cfg.DeleteMode, "delete-mode", "trash", "Delete mode: trash/hard")
	fs.BoolVar(&cfg.ImportRemote, "import-remote", false, "Import remote-only untracked files")
	fs.StringVar(&cfg.InitStrategy, "init-strategy", "skip-conflicts", "First-run strategy")
	fs.StringVar(&cfg.IgnoreFile, "ignore-file", "", "Path to ignore file")
	fs.BoolVar(&cfg.IncludeHidden, "include-hidden", false, "Include hidden files")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Print sync plan without mutation")
	fs.DurationVar(&cfg.TempTTL, "temp-ttl", 24*time.Hour, "Stale local temp TTL")
	fs.DurationVar(&cfg.RemoteTempTTL, "remote-temp-ttl", 24*time.Hour, "Stale remote temp TTL")
	fs.BoolVar(&cfg.ShowCleanup, "show-cleanup", false, "Show cleanup actions in dry-run")
	fs.Int64Var(&cfg.Bandwidth, "bandwidth", 0, "Max bytes/sec per transfer")
	fs.BoolVar(&cfg.Verbose, "verbose", false, "Enable debug logging")
	fs.StringVar(&cfg.LogFile, "log-file", "", "Write structured logs to file")
	fs.StringVar(&cfg.UIAddr, "ui-addr", "127.0.0.1:8787", "Local web UI listen address")
	fs.StringVar(&cfg.RemoteCachePath, "remote-cache", "", "Remote metadata cache path")
	fs.BoolVar(&cfg.ShowVersion, "version", false, "Print version")
	fs.StringVar(&cfg.StatePath, "state", "", "State file path")
	fs.BoolVar(&cfg.ShowLaunchAgent, "print-launch-agent", false, "Print LaunchAgent plist template")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.ShowVersion {
		return cfg, nil
	}
	if err := ResolveCredentials(&cfg); err != nil {
		return cfg, err
	}
	if cfg.ShowLaunchAgent {
		return cfg, nil
	}
	if cfg.LocalRoot == "" {
		return cfg, fmt.Errorf("-local is required")
	}
	if cfg.Token != "" && cfg.Pass == "" {
		cfg.Pass = cfg.Token
	}
	switch cfg.ConflictStrategy {
	case "skip", "local", "remote":
	default:
		return cfg, fmt.Errorf("invalid -conflict %q", cfg.ConflictStrategy)
	}
	switch cfg.DeleteMode {
	case "trash", "hard":
	default:
		return cfg, fmt.Errorf("invalid -delete-mode %q", cfg.DeleteMode)
	}
	switch cfg.InitStrategy {
	case "skip-conflicts", "local-wins", "remote-wins", "newer-wins":
	default:
		return cfg, fmt.Errorf("invalid -init-strategy %q", cfg.InitStrategy)
	}
	if cfg.RemoteWorkers < 1 {
		return cfg, fmt.Errorf("-remote-workers must be >= 1")
	}
	if cfg.Concurrency < 1 {
		return cfg, fmt.Errorf("-concurrency must be >= 1")
	}
	return cfg, nil
}
