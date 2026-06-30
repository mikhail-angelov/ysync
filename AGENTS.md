# ysync — Codebase Overview for AI Agents

## Project Summary

**ysync** is a Go service for macOS that synchronizes a local folder with Yandex Disk via the WebDAV API. It runs as a background LaunchAgent, watches local filesystem changes via `fsnotify`, compares local files with remote WebDAV metadata using a three-way state-aware comparator, and performs safe upload, download, delete, and conflict handling operations.

Version: `0.1.0` (defined in `ysync/types.go`)

---

## Architecture Overview

```
fsnotify watcher → ignore filter → debouncer → scanner → comparator → plan → scheduler → transfer
                                                                                 ↑
                                                    state store ←────────────────┘
```

The system is organized as a pipeline where:

1. **Watcher** monitors local filesystem events (fsnotify) with polling fallback on file descriptor exhaustion
2. **Ignorer** drops ignored paths before they enter the debouncer
3. **Debouncer** waits for files to be stable (unchanged size + modTime for a configurable duration)
4. **Scanner** builds local and remote file trees
5. **Comparator** performs a three-way diff between previous sync state, current local tree, and current remote tree
6. **Plan** is a list of actions (upload/download/delete/trash/conflict)
7. **Scheduler** executes plan items concurrently with path-level locking
8. **Transfer** handles actual file I/O and state updates

---

## File Map

### `main.go`
Entry point. Parses CLI flags via `ysync.ParseConfig`, calls `ysync.Run`. If `--version`, prints version and exits.

### `ysync/types.go`
Core type definitions:
- `LocalEntry` — path, isDir, modTime, size
- `RemoteEntry` — same + ETag
- `SyncState` — version, remoteRoot, localRoot, lastSyncedAt, entries map
- `SyncEntry` — per-file tracked state (local+remote mtime/size/etag, status, lastSyncedAt)
- `PlanItem` — Action + Path + Reason
- Action constants: `upload`, `download`, `conflict`, `trash-remote`, `trash-local`, `delete-remote`, `delete-local`, `ignore`, `skip`, `mark-synced`, `cleanup`
- Status constants: `synced`, `pending_upload`, `pending_download`, `conflict`, `deleted_local`, `deleted_remote`, `error`
- `normalizeTime(t)` — truncates to seconds, UTC

### `ysync/config.go`
CLI flag parsing using Go's `flag` package. Defines all configuration options (30+ flags). Validates conflict strategy, delete mode, init strategy, worker counts. Calls `ResolveCredentials` to populate user/pass from Keychain or env vars.

Key defaults: interval=5m, debounce=1m, conflict=skip, delete=true, delete-mode=trash, concurrency=3, remote-workers=4, init-strategy=skip-conflicts.

### `ysync/run.go`
Top-level orchestrator (`Run` function). Flow:
1. Create file logger
2. If `--print-launch-agent`, print plist and exit
3. If not `--dry-run`, start service mode: local watcher + auto-sync worker + local web UI, then block
4. In `--dry-run`, load ignore matcher
5. Load sync state from disk
6. Ensure state roots match current config
7. Cleanup local stale temp files
8. Scan local filesystem
9. If credentials present, create WebDAV client, load remote tree, cleanup remote temps, re-scan if mutated
10. Compare local/remote/state → plan
11. Print dry-run plan and exit

### `ysync/comparator.go`
Three-way comparison engine. Key types:
- `CompareOptions` — ImportRemote, InitStrategy, Delete, DeleteMode
- `Compare(state, locals, remotes, opts)` — builds scoped path set (locals + state entries + optional remote import), iterates sorted paths, delegates to `compareUntracked` or `compareTracked`
- `compareUntracked` — first-run decision logic (init strategy: skip-conflicts, local-wins, remote-wins, newer-wins)
- `compareTracked` — uses `localDiffers`/`remoteDiffers` vs previous state to decide action
- `localDiffers` — compares isDir, size, modTime vs prev state
- `remoteDiffers` — compares isDir, size, ETag, modTime vs prev state
- `sameInitial` — first-run matching check (same isDir + same size)
- `timeEqual` — uses 30-second tolerance window
- `isServiceRemoteTemp` — checks `*.ysync-uploading` pattern

### `ysync/transfer.go`
Execution engine. Key functions:
- `ExecutePlan` — iterates plan items, dispatches to uploadPath/downloadPath/trashRemotePath/deleteRemotePath/trashLocalPath/deleteLocalPath, marks state errors on failure
- `uploadPath` — point-refresh before upload, PUT to .ysync-uploading temp, MOVE to final, refresh both sides, update state
- `downloadPath` — GET with Range resume support, verify size, atomic rename, set mtime via Chtimes, refresh state
- `trashRemotePath` — point-refresh, MOVE to /ysync-trash/<ts>/<path>
- `deleteRemotePath` — point-refresh, DELETE
- `trashLocalPath` — check local unchanged, os.Rename to ~/.ysync/trash/<ts>/<path>
- `deleteLocalPath` — check local unchanged, os.Remove
- `readLocalEntry` — stats a local path
- `expectWebDAVStatus` — checks HTTP 2xx
- `markStateError`/`markStateStatus` — update state entry

Safety mechanisms: point-refresh before upload/delete, conflict detection on concurrent modification, unchanged verification before local delete, state update only after verified success.

### `ysync/webdav.go`
WebDAV HTTP client. Base URL + remote root path combination logic.
- `Propfind` — PROPFIND with configurable Depth header
- `Get` — GET with optional Range header for resume
- `Put` — PUT with body
- `Delete` — DELETE
- `Move` — MOVE with Destination and Overwrite headers (T/F)
- Convenience methods: `MoveUploadTempToFinal` (Overwrite: T), `MoveOriginalToConflict` (Overwrite: F), `MoveOriginalToTrash` (Overwrite: F), `MoveStaleTempToTrash` (Overwrite: F)
- `remoteURL` — constructs full URL from remote root + relative path, path segment handling
- `cleanRemoteRoot` — normalizes remote root path

### `ysync/remote.go`
Remote tree loading. Key functions:
- `LoadRemoteTree` — starts recursive Depth:1 crawl with worker pool
- `RefreshRemotePath` — single-path PROPFIND with 404 handling, returns (RemoteEntry, bool, error)
- `propfindEntries` — PROPFIND with retry (3 attempts, exponential backoff 100ms→200ms→400ms)
- `crawlRemoteDepth1` — worker pool BFS crawl: enqueue root, fetch Depth:1, discover directories, enqueue subdirectories
- XML parsing: `multistatusXML`, `responseXML`, `propstatXML`, `propXML`, `responseToRemoteEntry`, `remoteRelFromHref`
- Path handling: `unescapePathSegments` (URL-decode segment by segment to avoid slash-in-filename issues)

### `ysync/local.go`
Local filesystem scanner.
- `ScanLocal` — `filepath.WalkDir`, skips root, uses `os.Lstat` for symlink detection (skips with warning), `os.Stat` for metadata, applies ignore matcher
- `SortedLocalPaths` — returns sorted path keys
- `fileSize` — 0 for directories

### `ysync/state.go`
Sync state persistence.
- `DefaultStatePath` — `~/.ysync/state.json`
- `LoadState` — reads JSON file, returns empty state on NotExist, validates version
- `SaveStateAtomic` — writes to .tmp file, fsync, rename (atomic write pattern)
- `EntryFromMetadata` — creates SyncEntry from LocalEntry + RemoteEntry + status
- `EnsureStateRoots` — validates state matches current config

### `ysync/watcher.go`
File watcher with degraded mode fallback.
- Modes: `fsnotify` (preferred) or `polling` (on EMFILE/ENFILE)
- `StartLocalWatcher` — tries fsnotify, falls back to polling on file limit errors
- Recursive watch setup: `watchRecursive` walks directories and adds watchers
- Event handling: ignores symlinks/ignored paths, adds watchers for new dirs, removes watchers for deleted dirs, emits immediately on Remove/Rename, otherwise debounces
- Polling fallback: `poll` runs `ScanLocal` at interval, emits changes via `emitChangedLocalPaths`
- `emitChangedLocalPaths` — compares previous map vs current, calls emitChanged/emitDeleted
- `isFileLimitError` — checks EMFILE/ENFILE or "too many open files" string

### `ysync/ignore.go`
Ignore pattern matching (gitignore-style globs).
- Default patterns: .DS_Store, Thumbs.db, Desktop.ini, .localized, ._\*, .Trashes, .git/, .svn/, .hg/, node_modules/, vendor/, *.ysync.\*, *.conflict-local-\*, etc.
- Hidden files excluded by default (.\* pattern) unless `--include-hidden`
- Custom ignore file support via `--ignore-file`
- `Match` — checks path against all patterns, supports directory-only patterns (trailing /)
- `matchIgnorePattern` — handles `/` suffix for dir-only, matches both base name and relative path
- `cleanRel` — filepath.ToSlash + filepath.Clean + NFC normalization

### `ysync/scheduler.go`
Concurrent job scheduler.
- Path-level locking: each job has keys (paths), only one job per key can be queued/active
- `NewScheduler(concurrency)` — starts N worker goroutines
- `Schedule` — acquires locks, enqueues job, signals worker
- `StopScheduling` — prevents new jobs, unlocks pending
- `Shutdown` — graceful shutdown with context timeout
- `Job` — Kind (sync/cleanup), Path, Keys, Run function

### `ysync/debounce.go`
Per-path stable file debouncer.
- `StableDebouncer` — root path, delay, pending map with per-path channels
- `Notify` — starts/resets debounce timer for path
- `EmitNow` — immediately fires callback for path (used on delete/rename)
- `waitStable` — takes two snapshots separated by delay, fires callback if identical
- `snapshot` — stats path, returns size + modTime
- `sameFileSnapshot` — compares size + modTime equality

### `ysync/cleanup.go`
Stale temp file cleanup.
- `CleanupLocalTemps` — walks local root, finds stale *.ysync.tmp / *.ysync.part older than TTL, removes them
- `CleanupRemoteTemps` — iterates remote entries, finds stale *.ysync-uploading older than TTL, moves to remote trash
- `isStaleLocalTempCandidate` — matches *.ysync.tmp and *.ysync.part
- `remoteTempTrashPath` — `/ysync-trash/temp/<ts>/<path>`

### `ysync/notify.go`
macOS user notifications via AppleScript (`osascript`).
- `runNotification` — overridable var for testing
- `NotifyConflict` — displays "ysync conflict" notification with path
- `escapeAppleScript` — escapes backslashes and quotes

### `ysync/log.go`
Structured logger.
- Levels: DEBUG (verbose only), INFO, WARN, ERROR
- Format: `RFC3339 [LEVEL] msg key=value key=value`
- Supports file output via `--log-file`
- `expandHome` — expands `~` prefix

### `ysync/keychain.go`
macOS Keychain credential retrieval via `security` CLI.
- `ResolveCredentials` — priority: keychain profile → default keychain → env vars → CLI flags
- `readKeychainCredentials` — reads `yandex_user` and `yandex_pass` from Keychain
- `readKeychainPassword` — `security find-generic-password` command

### `ysync/launchagent.go`
LaunchAgent plist generation (referenced by `GenerateLaunchAgentPlist` in run.go).

### `ysync/signal.go`
OS signal handling: `ShutdownSignalContext` creates context cancelled on SIGINT/SIGTERM.

### `ysync/ui.go`
Web UI server (1400 lines). HTMX-lite frontend with Go `html/template` rendering.
- Pages: Dashboard, Plan, Conflicts, Tree, Operations, Settings
- Features: remote tree caching with TTL, plan filtering, confirmation tokens for destructive actions, open-in-Finder, per-path actions (upload/download/trash/mark-synced)
- Routes: `/`, `/plan`, `/conflicts`, `/tree`, `/operations`, `/settings`, `/actions/refresh`, `/actions/run-plan`, `/actions/path/*`
- HTMX-lite JS (~100 lines) embedded in Go source
- CSS-in-HTML templates embedded in Go source

### `ysync/remote_cache.go`
Remote tree cache with TTL. Validates BaseURL + RemoteRoot match. Used by UI server to avoid frequent remote scans.

---

## Guiding Principles

The codebase follows these principles in priority order:

1. **KISS (Keep It Simple, Stupid)** — Simplicity over cleverness. Straightforward logic, minimal indirection, flat package structure, no unnecessary abstractions. Each function does one thing clearly.

2. **YAGNI (You Ain't Gonna Need It)** — Do not build for speculative future requirements. Sync scope is local-only by default (not a full remote mirror). No chunked uploads, no rename detection, no SQLite state store. Add features only when the need is proven.

3. **SOLID (nice-to-have)** — Applied pragmatically where it doesn't conflict with KISS or YAGNI. Single-responsibility functions, explicit dependencies via parameters rather than global state, clear interfaces (e.g. `Logger`, `IgnoreMatcher`). Over-engineering with deep interface hierarchies or excessive abstraction is avoided.

## Key Design Decisions

1. **Local-scoped sync**: Only files that exist locally OR are in previous sync state are tracked. Remote-only files are ignored by default. This prevents downloading an entire Yandex Disk to a small local disk.

2. **Three-way comparison**: Previous sync state + current local + current remote. Never compares local vs remote directly (which would cause false conflicts on first run).

3. **Safe transfers via temp files**: Uploads go to `.ysync-uploading` then MOVE to final. Downloads go to `.ysync-downloading` then atomic rename + Chtimes. This prevents partial files from being treated as complete.

4. **Point-refresh before dangerous ops**: Before upload or remote delete/trash, a single-path PROPFIND confirms the remote hasn't changed since the last scan. If it has, the operation is cancelled and marked as conflict.

5. **Atomic state saves**: Write to .tmp, fsync, rename. Prevents state corruption on crash.

6. **Path-level job locking**: Scheduler ensures one active operation per path, preventing race conditions.

7. **Degraded mode on file descriptor exhaustion**: fsnotify uses kqueue on macOS which has per-process file limits. On EMFILE/ENFILE, automatically falls back to polling mode.

8. **30-second timestamp tolerance**: Avoids spurious conflicts from filesystem timestamp rounding issues.

---

## Data Flow

### Dry-run sync (`ysync -local ... --dry-run`):
```
ParseConfig → Run → LoadState → CleanupLocalTemps → ScanLocal → LoadRemoteTree → CleanupRemoteTemps → Compare → PrintPlan
```

### Service mode (default):
```
ServeUI → local watcher → auto-sync non-conflict plan items → HTTP server with tree/action endpoints
```

### File watcher → sync trigger:
```
fsnotify event → ignore filter → debounce → stable check → auto-sync cycle
```

---

## Configuration Quick Reference

| Flag | Default | Description |
|------|---------|-------------|
| -local | (required) | Local folder to sync |
| -remote | / | Remote Yandex Disk folder |
| -conflict | skip | Conflict strategy: skip/local/remote |
| -delete | true | Enable delete sync for tracked files |
| -delete-mode | trash | Delete mode: trash/hard |
| -import-remote | false | Import remote-only untracked files |
| -init-strategy | skip-conflicts | First-run strategy |
| -interval | 5m | Remote scan/cache TTL |
| -debounce | 1m | File debounce delay |
| -concurrency | 3 | Max concurrent operations |
| -dry-run | false | Print plan without mutation |
| -ui-addr | 127.0.0.1:8787 | Local web UI listen address |
| -verbose | false | Enable debug logging |

Environment variables: `YANDEX_USER`, `YANDEX_PASS`, `YANDEX_TOKEN`

---

## Sync Decision Table

| Condition | Action |
|-----------|--------|
| Local new file, not remote | Upload |
| Remote-only untracked file | Ignore (default) |
| Remote-only with --import-remote | Download |
| Local changed, remote unchanged | Upload |
| Remote changed, local unchanged | Download |
| Both changed | Conflict |
| Local deleted, remote unchanged | Trash/delete remote |
| Remote deleted, local unchanged | Trash/delete local |
| Local deleted, remote changed | Conflict |
| Remote deleted, local changed | Conflict |
| Both unchanged | Skip |
| First run, local=remote size | Mark synced |
| First run, differing | Depends on init-strategy |

---

## Testing

Test files are adjacent to source files with `_test.go` suffix:
- `comparator_test.go` — decision table tests
- `transfer_test.go` — transfer logic tests
- `ignore_test.go` — pattern matching tests
- `state_test.go` — state persistence tests
- `local_test.go` — local scanner tests
- `remote_test.go` — remote tree loading tests
- `watcher_test.go` — watcher tests
- `debounce_test.go` — debouncer tests
- `scheduler_test.go` — scheduler tests
- `cleanup_test.go` — cleanup tests
- `webdav_test.go` — WebDAV client tests
- `keychain_test.go` — keychain tests
- `launchagent_test.go` — launch agent tests
- `log_test.go` — logger tests
- `notify_test.go` — notification tests
- `remote_cache_test.go` — remote cache tests
- `ui_test.go` — UI server tests
