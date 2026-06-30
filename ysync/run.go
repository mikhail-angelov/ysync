package ysync

import (
	"context"
	"fmt"
	"io"
	"os"
)

func Run(ctx context.Context, cfg Config) error {
	log, closeLog, err := NewFileLogger(cfg.LogFile, cfg.Verbose)
	if err != nil {
		return err
	}
	if closeLog != nil {
		defer closeLog.Close()
	}
	if cfg.ShowLaunchAgent {
		exe, err := os.Executable()
		if err != nil {
			exe = "/usr/local/bin/ysync"
		}
		fmt.Fprint(os.Stdout, GenerateLaunchAgentPlist(cfg, exe))
		return nil
	}
	if !cfg.DryRun {
		return ServeUI(ctx, cfg, log)
	}
	ignores, err := NewIgnoreMatcher(cfg.IgnoreFile, cfg.IncludeHidden)
	if err != nil {
		return err
	}
	state, err := LoadState(cfg.StatePath)
	if err != nil {
		return err
	}
	if err := EnsureStateRoots(state, cfg.LocalRoot, cfg.RemoteRoot); err != nil {
		return err
	}
	cleanupPlan, err := CleanupLocalTemps(cfg.LocalRoot, cfg.TempTTL, cfg.DryRun, log)
	if err != nil {
		return err
	}
	locals, err := ScanLocal(cfg.LocalRoot, ignores, log)
	if err != nil {
		return err
	}

	remotes := map[string]RemoteEntry{}
	var client *WebDAVClient
	if cfg.User != "" || cfg.Pass != "" {
		client, err = NewWebDAVClient(cfg.BaseURL, cfg.RemoteRoot, cfg.User, cfg.Pass, nil)
		if err != nil {
			return err
		}
		client.Log = log
		remotes, err = LoadRemoteTree(ctx, client, cfg.RemoteWorkers, log)
		if err != nil {
			return err
		}
		remoteCleanupPlan, mutated := CleanupRemoteTemps(ctx, client, cfg.RemoteRoot, remotes, cfg.RemoteTempTTL, cfg.DryRun, log)
		cleanupPlan = append(cleanupPlan, remoteCleanupPlan...)
		if mutated {
			remotes, err = LoadRemoteTree(ctx, client, cfg.RemoteWorkers, log)
			if err != nil {
				return err
			}
		}
	} else {
		log.Debug("skipping remote scan without credentials")
	}
	plan := Compare(state, locals, remotes, CompareOptions{
		ImportRemote: cfg.ImportRemote,
		InitStrategy: cfg.InitStrategy,
		Delete:       cfg.Delete,
		DeleteMode:   cfg.DeleteMode,
	})
	if cfg.Verbose || cfg.ShowCleanup {
		PrintPlan(os.Stdout, cleanupPlan, cfg.Verbose)
	}
	PrintPlan(os.Stdout, plan, cfg.Verbose)
	return nil
}

func PrintPlan(w io.Writer, plan []PlanItem, verbose bool) {
	for _, item := range plan {
		if item.Action == ActionIgnore && !verbose {
			continue
		}
		fmt.Fprintf(w, "[%-12s] %-40s %s\n", item.Action, item.Path, item.Reason)
	}
}
