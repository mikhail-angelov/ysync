# ysync — Yandex Disk WebDAV Sync for macOS

`ysync` synchronizes a local folder with Yandex Disk via the WebDAV API. It runs as a background LaunchAgent, watches filesystem changes with `fsnotify`, and safely syncs files with conflict detection.

## Quick Start

```bash
# Install
git clone https://github.com/mikhail-angelov/ysync.git
cd ysync
go build -o ysync .

# First run (dry-run mode — no changes made)
ysync -local ~/SyncFolder -remote / --dry-run

# Run service with web UI
ysync -local ~/SyncFolder -remote /
# UI at http://127.0.0.1:8787
```

## Requirements

- macOS (uses `kqueue` for file watching, Keychain for credentials)
- Yandex Disk with WebDAV enabled
- Go 1.21+ (to build from source)

## Authentication

Priority order (first available wins):

1. **macOS Keychain** — store `yandex_user` and `yandex_pass` in your default Keychain
2. **Environment variables** — `YANDEX_USER` and `YANDEX_PASS`
3. **CLI flags** — `--user` and `--pass`

## Usage

```bash
ysync -local ~/SyncFolder -remote / --user your@email.com --pass your-password
```

### Common options

| Flag | Default | Description |
|------|---------|-------------|
| `-local` | required | Local folder to sync |
| `-remote` | `/` | Remote Yandex Disk folder |
| `-conflict` | `skip` | Conflict strategy: `skip`/`local`/`remote` |
| `-delete` | `true` | Enable delete sync for tracked files |
| `-delete-mode` | `trash` | Delete mode: `trash` (safe) or `hard` |
| `-import-remote` | `false` | Import remote-only files |
| `-init-strategy` | `skip-conflicts` | First-run strategy |
| `-interval` | `5m` | Remote scan interval / cache TTL |
| `-debounce` | `1m` | File debounce delay |
| `-concurrency` | `3` | Max concurrent operations |
| `-dry-run` | `false` | Print plan without making changes |
| `-ui-addr` | `127.0.0.1:8787` | Web UI address |
| `-verbose` | `false` | Enable debug logging |
| `-log-file` | `""` | Write logs to file |

### Examples

```bash
# Dry run to see what would happen
ysync -local ~/Documents/ysync -remote /docs --dry-run

# Run with verbose logging
ysync -local ~/Documents/ysync -remote /docs --verbose

# Force local files to win on conflicts
ysync -local ~/Documents/ysync -remote /docs --conflict local

# Delete remote files permanently (no trash)
ysync -local ~/Documents/ysync -remote /docs --delete-mode hard

# Download remote-only files
ysync -local ~/Documents/ysync -remote /docs --import-remote

# Override login credentials
YANDEX_USER=user@yandex.com YANDEX_PASS=supersecret ysync -local ~/SyncFolder
```

### First sync strategies

On first run, `ysync` has no previous sync state. Use `--init-strategy` to control behavior:

| Strategy | Behavior |
|----------|----------|
| `skip-conflicts` (default) | Upload new local files. Skip files that exist both locally and remotely with different metadata. |
| `local-wins` | Upload local version for all conflicting files |
| `remote-wins` | Download remote version for all conflicting files |
| `newer-wins` | Use whichever file was modified more recently |

Recommended: run `--dry-run` first to review the plan before committing.

```bash
ysync -local ~/SyncFolder --dry-run
ysync -local ~/SyncFolder --dry-run --init-strategy newer-wins
```

## Web UI

When the service is running, open [http://127.0.0.1:8787](http://127.0.0.1:8787) in your browser.

### Tree view
Browse your synced files and folders. Click folders to expand them.

**Status icons:**

| Icon | Meaning |
|------|---------|
| ✓ | Synced (local = remote) |
| ↑ | Pending upload or local-only |
| ↓ | Pending download |
| ⚠ | Conflict |
| 🗑 | Pending delete / trash |
| 👁 | Remote-only (ignored) |
| ! | Error |

**Actions per file:**

| Button | Description |
|--------|-------------|
| Sync | Download remote-only file |
| Keep local / Keep remote | Resolve a conflict |
| Mark synced | Mark as synced when sizes match |
| Delete | Trash/delete (requires confirmation) |
| Open | Reveal in Finder |

### Operations tab
Shows a log of all operations during the current session.

### Settings tab
Shows current configuration.

### Filters
Use the search bar to filter files by name. Toggle `Local only` or `Conflicts` checkboxes.

## How it works

### Sync model
`ysync` is **local-scoped** — it only syncs files that exist locally or were previously synced. Remote-only files are ignored by default (`--import-remote` to change this). This prevents downloading an entire Yandex Disk to a small disk.

### Safety
- **Temp files**: Uploads go to `.ysync-uploading` then renamed. Downloads go to `.ysync-downloading` then renamed. Partial files are never treated as complete.
- **Point-refresh**: Before any destructive operation (upload, delete), a single-path PROPFIND verifies the remote hasn't changed.
- **Atomic state saves**: State is written to `.tmp`, fsynced, then renamed.
- **Conflict detection**: If both local and remote changed since last sync, the file is marked as conflict and left untouched.

### Auto-sync
When running in service mode, `ysync` watches local filesystem changes, debounces them (default: 1 minute), and automatically executes non-conflict plan items (upload, download, delete).

### File ignoring
Default ignored patterns: `.DS_Store`, `Thumbs.db`, `.git/`, `node_modules/`, `*.ysync.*`, `*.conflict-*`, and hidden files (files starting with `.`). Use `--include-hidden` to include hidden files, or `--ignore-file` for custom patterns.

## Installation as LaunchAgent

To run `ysync` as a background service:

```bash
# Print the LaunchAgent plist
ysync --print-launch-agent
```

Save the output to `~/Library/LaunchAgents/com.ysync.plist`, adjust paths, then:

```bash
launchctl load ~/Library/LaunchAgents/com.ysync.plist
```

## Project structure

```
ysync/
  main.go               — Entry point
  ysync/
    config.go           — CLI flag parsing
    run.go              — Orchestrator
    watcher.go          — File watcher with fallback polling
    debounce.go         — File stability debouncer
    ignore.go           — Ignore pattern matching
    local.go            — Local file scanner
    remote.go           — Remote tree loader
    webdav.go           — WebDAV HTTP client
    comparator.go       — Three-way comparison engine
    transfer.go         — Upload/download/delete execution
    state.go            — Sync state persistence
    scheduler.go        — Concurrent job scheduler
    cleanup.go          — Stale temp file cleanup
    notify.go           — macOS notifications
    ui.go               — Web UI server
    remote_cache.go     — Remote tree cache
    keychain.go         — macOS Keychain integration
    launchagent.go      — LaunchAgent plist generation
    log.go              — Structured logger
    signal.go           — OS signal handling
```

## Configuration reference

### Environment variables

| Variable | Description |
|----------|-------------|
| `YANDEX_USER` | Yandex login email |
| `YANDEX_PASS` | Yandex password |

### Keychain setup

```bash
security add-generic-password -s ysync -a yandex_user -w "your@email.com"
security add-generic-password -s ysync -a yandex_pass -w "your-password"