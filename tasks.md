# ysync task list

Status legend: `[ ]` pending, `[~]` partial, `[x]` done.

## Phase 1 - Core Data Model

- [x] Scaffold Go module and CLI entrypoint.
- [x] Parse CLI flags, environment auth fallbacks, and conservative defaults.
- [x] Implement default/custom ignore pattern loading.
- [x] Implement local tree scanner with UTC second timestamp normalization and symlink skip warnings.
- [x] Implement sync state model and atomic JSON save/load.

## Phase 2 - Comparator and Dry Run

- [x] Implement three-way comparison using previous state, current local tree, and current remote tree.
- [x] Enforce local-scoped sync rules: remote-only untracked paths are ignored by default.
- [x] Enforce service temp rules: `.ysync-uploading` is ignored in normal sync and never imported.
- [x] Implement first-run strategies for `skip-conflicts`, `local-wins`, `remote-wins`, and `newer-wins`.
- [x] Implement dry-run output for upload, download, conflict, trash/delete, ignore, mark-synced, and skip actions.

## Phase 3 - WebDAV Client

- [x] Implement Basic Auth request handling.
- [x] Implement explicit token behavior as Basic Auth password when `-user` is also provided.
- [x] Implement PROPFIND, GET, PUT, MOVE, and DELETE request primitives.
- [x] Ensure MOVE temp-to-final uses `Overwrite: T`.
- [x] Ensure MOVE conflict/trash/temp-cleanup uses `Overwrite: F`.
- [x] Test Destination header URL construction.
- [x] Implement remote Depth: infinity scan with recursive Depth: 1 fallback and retry/backoff.

## Phase 4 - Transfers

- [x] Implement safe upload via remote temp path and final MOVE.
- [x] Implement required point refresh before upload.
- [x] Implement safe download via local temp path, size verification, mtime restore, and optional Range resume.
- [x] Update state only after confirmed local and remote metadata refresh.

## Phase 5 - Delete / Trash

- [x] Implement tracked local delete to remote trash with point refresh.
- [x] Implement tracked remote delete to local trash.
- [x] Implement hard delete as explicit opt-in only.
- [x] Ensure remote trash root is outside the configured sync root.

## Phase 6 - Cleanup

- [x] Implement stale local temp cleanup.
- [x] Implement stale remote `.ysync-uploading` cleanup to remote trash.
- [x] Add cleanup dry-run output.
- [x] Invalidate remote cache after remote cleanup.

## Phase 7 - Watcher and Debouncer

- [x] Implement recursive fsnotify watcher.
- [x] Filter ignored paths before debounce.
- [x] Implement dual size/modTime stable-file debounce.
- [x] Detect EMFILE/ENFILE and switch to degraded polling mode.

## Phase 8 - Scheduler

- [x] Implement concurrency-limited queue.
- [x] Enforce one active operation per path.
- [x] Gracefully stop scheduling on SIGTERM/SIGINT.
- [x] Coordinate cleanup jobs with active sync jobs.

## Phase 9 - macOS Integration

- [x] Add LaunchAgent plist generation or template.
- [x] Add Keychain credential retrieval.
- [x] Add best-effort macOS notifications.
- [x] Add optional log file output.

## Phase 10 - Local Web UI MVP

- [x] Add opt-in localhost UI flags.
- [x] Serve server-rendered Dashboard, Plan, Conflicts, Tree, Operations Log, and Settings pages.
- [x] Render plan rows from the existing comparator output.
- [x] Render conflict details and safe resolution actions.
- [x] Render a lazy-loaded tree view with search and status filters.
- [x] Add POST-only refresh, run-plan, and per-path action endpoints.
- [x] Require server-side one-time confirmation tokens for dangerous actions.
- [x] Reuse existing sync executor safety checks for mutating actions.
- [x] Keep credentials out of browser-visible URLs and storage.
- [x] Persist remote metadata cache across UI restarts.
- [x] Block plan rendering when remote scan fails and no cache is available.
