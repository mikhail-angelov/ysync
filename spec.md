# ysync — Yandex Disk WebDAV Local-Scoped Sync Service

## 1. Overview

`ysync` is a Go service for macOS that synchronizes a local folder with Yandex Disk via the WebDAV API.

The service runs as a background macOS LaunchAgent, watches local filesystem changes, compares local files with remote WebDAV metadata, and performs safe upload, download, delete, and conflict handling operations.

Important: `ysync` is **not a full remote mirror** by default.

The service syncs only:

* files that currently exist locally;
* new files created locally;
* files previously synced and stored in local sync state.

Remote-only files that were never present locally and are not tracked in sync state are ignored by default.

This prevents the service from accidentally downloading the entire Yandex Disk remote folder to a limited local disk.

Optional remote import can be enabled explicitly:

```bash
--import-remote
```

---

## 2. Goals

The service must:

* run as a macOS background service;
* sync a local folder with a Yandex Disk WebDAV folder;
* watch local file changes using `fsnotify`;
* debounce file changes to avoid syncing incomplete writes;
* compare local, remote, and previous sync state;
* upload new or changed local files;
* download changed remote files only when they are already tracked;
* ignore remote-only untracked files by default;
* sync deletes for tracked files safely;
* detect conflicts safely;
* support dry-run mode;
* handle interrupted transfers safely;
* store credentials securely via macOS Keychain where possible;
* provide structured logs and macOS user notifications.

---

## 3. Non-Goals for MVP

The MVP does not include:

* full remote mirror by default;
* generic WebDAV provider support;
* Google Drive support;
* Nextcloud support;
* Yandex Disk Trash API integration;
* macOS Menu Bar app;
* true rename detection;
* chunked or resumable upload;
* file content hashing for every file;
* real-time remote push notifications;
* multi-user sync;
* sync conflict UI.

---

## 4. Sync Model

The service uses a **local-scoped bidirectional sync model**.

The local folder defines the sync boundary.

A path is inside sync scope if:

1. it currently exists in the local tree;
2. or it exists in the previous sync state.

Watcher events do **not** define sync scope permanently. They only trigger re-evaluation of local paths.

Effective sync scope:

```text
current local paths ∪ previous sync state paths
```

Remote-only untracked files are ignored by default.

A remote file is ignored if:

1. it exists only in Yandex Disk;
2. it does not exist locally;
3. it does not exist in the local sync state;
4. `--import-remote` is not enabled.

Default behavior:

```text
remote-only untracked file → ignore
local-only untracked file → upload
tracked remote change → download
tracked local change → upload
tracked local delete → move remote to trash
tracked remote delete → move local to trash
tracked both-side change → conflict
```

Optional remote import:

```bash
--import-remote
```

Default:

```bash
--import-remote=false
```

When enabled, remote-only untracked files are downloaded and added to sync state.

---

## 5. High-Level Architecture

```text
fsnotify watcher
    │
    ▼
ignore filter
    │
    ▼
debouncer
    │
    ▼
local scanner
    │
    ▼
remote scanner / cache
    │
    ▼
state-aware comparator
    │
    ▼
sync planner
    │
    ├─► upload queue
    │
    ├─► download queue
    │
    ├─► conflict handler
    │
    └─► delete handler
    │
    ▼
state store
```

Main modules:

```text
watcher       — local fsnotify recursive watching
ignore        — default and custom ignore patterns
debouncer     — per-path debounce and stable-file detection
local scanner — local tree scan with size and UTC modTime
remote client — WebDAV PROPFIND / GET / PUT / MOVE / DELETE
cache         — remote tree cache with TTL and invalidation
state store   — last known synced state
comparator    — local/current/remote/previous comparison
planner       — sync plan generation
scheduler     — upload/download/conflict/delete execution
notifier      — macOS user notifications
logger        — structured logging
```

---

## 6. File Watching

Use `fsnotify` to watch the local folder recursively.

Important macOS note: `fsnotify` on macOS uses the `kqueue` backend, not FSEvents.

Requirements:

* watch all existing subdirectories;
* when a new directory is created locally, add a watcher for it;
* when a watched directory is removed or renamed, remove/update its watcher;
* coalesce duplicate events for the same path;
* filter ignored paths before they enter the debouncer;
* debounce file changes for 1 minute after the last detected modification;
* do not sync a file while it is still being written;
* a file is considered stable only when both size and modTime stay unchanged for the debounce duration.

Event processing pipeline:

```text
fsnotify event
    ↓
normalize path
    ↓
ignore check
    ↓
debounce
    ↓
stable file check
    ↓
enqueue sync planning
```

Ignored files must be dropped immediately in `watcher.go` or before `debounce.go`.

This prevents `.DS_Store`, AppleDouble files, temporary files, conflict files, and service-generated files from creating lock contention, debounce noise, or unnecessary state records.

---

## 7. Debouncing and Stable File Detection

Default debounce:

```bash
--debounce 1m
```

A file is stable only if both size and modTime remain unchanged for the debounce duration.

Stable file check:

```text
stat #1: size + modTime
wait debounce duration
stat #2: size + modTime
if size and modTime are unchanged → stable
else reset debounce timer
```

If a file cannot be read because another process is still writing it, do not treat it as a fatal error.

The path should remain pending and be retried after the debounce delay.

---

## 8. File Descriptor Exhaustion

Recursive watching may fail on large sync trees because macOS has per-process open file descriptor limits.

This problem is not limited to deep directory structures. On macOS with the `kqueue` backend, a large total number of files and directories can exhaust file descriptors.

`watcher.go` must detect:

```text
EMFILE
ENFILE
too many open files
```

If watcher initialization fails because of file descriptor limits:

1. log a clear `ERROR`;
2. include the failed directory path;
3. explain that the user may need to increase macOS file limits;
4. continue in degraded polling mode instead of crashing.

Example log:

```text
[ERROR] watcher limit reached: too many open files path=/Users/user/SyncFolder
[WARN] switching to degraded polling mode; increase maxfiles via launchctl limit maxfiles if recursive watching is required
```

Fallback behavior:

```text
watcher unavailable or partially initialized → periodic local scan mode
```

Default local polling fallback interval:

```bash
--local-scan-interval 1m
```

---

## 9. Ignore Patterns

Default ignored paths:

```text
.DS_Store
Thumbs.db
Desktop.ini
.localized
._*
.Trashes
.git/
.svn/
.hg/
node_modules/
vendor/
*.ysync.tmp
*.ysync.part
*.ysync-downloading
*.ysync-uploading
*.conflict-local-*
*.conflict-remote-*
*.ysync-conflict-*
```

Important:

`*.ysync-uploading` is not a local startup temp file in the MVP. It is used as a remote temporary upload suffix.

However, it must still be ignored during both local and remote sync scans because it is a service-generated artifact.

Custom ignore file:

```bash
--ignore-file ~/.ysyncignore
```

Format:

```text
# comment
*.log
tmp/
.cache/
```

Ignore patterns apply to both local and remote paths.

Ignored paths must be filtered before debounce.

Symlinks are skipped with a warning.

Use `os.Lstat()` to detect symlinks.

Relative sync paths must be Unicode-normalized to NFC before ignore matching.

---

## 10. Local Tree Loading

Walk the local sync root and build local metadata.

Each local entry should include:

```go
type LocalEntry struct {
    Path    string
    IsDir   bool
    ModTime time.Time
    Size    int64
}
```

Local timestamp handling:

```go
localTime := info.ModTime().UTC().Truncate(time.Second)
```

Use `os.Stat()` for normal files and directories.

Use `os.Lstat()` first to skip symlinks.

Local relative paths must be slash-separated and Unicode-normalized to NFC before being used as map keys, compare keys, plan paths, or state paths.

---

## 11. Remote Tree Loading

Load remote metadata via WebDAV `PROPFIND`.

`PROPFIND Depth: infinity` is not authoritative for Yandex Disk.

Yandex may return `207 Multi-Status` for root `Depth: infinity` while omitting nested files. The implementation must not trust a successful `Depth: infinity` response as a complete tree.

Required mode:

```text
recursive PROPFIND Depth: 1
```

Optional diagnostic/optimization mode:

```text
PROPFIND Depth: infinity
```

Remote loading algorithm:

```text
use recursive Depth: 1 crawl as the authoritative remote tree
enqueue directories discovered by each Depth: 1 response
do not classify a local file as remote-missing based only on a shallow root response
```

Recursive crawl should:

* use a bounded worker pool;
* respect request concurrency limits;
* support cancellation via context;
* apply retries with exponential backoff;
* avoid flooding Yandex WebDAV.

Default remote crawl workers:

```bash
--remote-workers 4
```

Default retry policy:

```text
max attempts: 3
backoff: 1s → 2s → 4s
```

Each remote entry should include:

```go
type RemoteEntry struct {
    Path    string
    IsDir   bool
    ModTime time.Time
    Size    int64
    ETag    string
}
```

Remote timestamp parsing:

```go
remoteTime, err := time.Parse(time.RFC1123, raw)
remoteTime = remoteTime.UTC().Truncate(time.Second)
```

Remote href paths must be split into URL path segments first, URL-decoded segment by segment, converted to slash-separated relative paths, and Unicode-normalized to NFC before being used as map keys, compare keys, plan paths, or state paths.

Do not URL-decode the whole href path before splitting path segments. A remote filename can contain a literal percent-encoded slash, represented by WebDAV as `%252F`; decoding the whole path would turn that filename content into `%2F` before path cleaning and can make it behave like a directory separator.

This is required because macOS may expose decomposed filenames such as `и` + combining breve while Yandex WebDAV may return the visually equivalent precomposed character `й`. These paths must compare as the same sync path after NFC normalization.

Remote cache:

* TTL: 5 minutes by default;
* invalidated immediately after successful upload, download, delete, remote move, or remote temp cleanup;
* may contain remote-only files outside sync scope;
* comparator must ignore remote-only untracked files unless `--import-remote` is enabled.

Default:

```bash
--interval 5m
```

Remote scan failure must not corrupt sync state.

If remote scan fails:

```text
do not upload/delete based on incomplete remote data
mark sync cycle as failed
retry later
keep previous state unchanged
```

---

## 12. Required Point Refresh Before Dangerous Operations

The remote cache must not be trusted for dangerous operations.

Before upload or remote delete/trash, the service must refresh metadata for the exact remote path.

### Before upload

Before uploading a local file to an existing remote path:

1. perform `PROPFIND` for the exact remote path;
2. compare fresh remote metadata with the previous sync state;
3. if remote changed since state, cancel upload and mark conflict;
4. if remote did not change, proceed with upload.

This prevents overwriting a fresh remote change that has not yet appeared in the cached remote tree.

### Before remote delete/trash

Before deleting or moving a tracked remote file to trash:

1. perform `PROPFIND` for the exact remote path;
2. compare fresh remote metadata with the previous sync state;
3. if remote changed since state, cancel delete/trash and mark conflict;
4. if remote did not change, proceed with delete or move-to-trash.

### Point-refresh failure

If required point-refresh fails due to timeout, auth error, network error, `PROPFIND` error, malformed response, or any other remote metadata failure:

```text
do not upload
do not delete
do not move remote file to trash
do not update sync state
mark operation as retryable error
retry later
```

A point-refresh error must never be interpreted as “remote unchanged”.

### Before download

Before download, the service already uses remote metadata to select the remote version.

After download, remote metadata may come from:

* the fresh remote entry used to start the download;
* GET response headers such as `ETag`, `Last-Modified`, `Content-Length`, if available;
* fallback `PROPFIND` if required metadata is missing.

---

## 13. Sync State

The service must persist last known synced state.

Recommended state file:

```text
~/.ysync/state.json
```

State must store more than only modTime.

Recommended structure:

```go
type SyncState struct {
    Version      int                  `json:"version"`
    RemoteRoot   string               `json:"remote_root"`
    LocalRoot    string               `json:"local_root"`
    LastSyncedAt time.Time            `json:"last_synced_at"`
    Entries      map[string]SyncEntry `json:"entries"`
}

type SyncEntry struct {
    Path          string    `json:"path"`
    IsDir         bool      `json:"is_dir"`

    LocalModTime  time.Time `json:"local_mod_time"`
    LocalSize     int64     `json:"local_size"`

    RemoteModTime time.Time `json:"remote_mod_time"`
    RemoteSize    int64     `json:"remote_size"`
    RemoteETag    string    `json:"remote_etag"`

    Status        string    `json:"status"`
    LastSyncedAt  time.Time `json:"last_synced_at"`
}
```

Valid statuses:

```text
synced
pending_upload
pending_download
conflict
deleted_local
deleted_remote
error
```

The sync state is used to detect:

* local-only changes;
* remote-only changes;
* both-sides changes;
* local deletion;
* remote deletion;
* conflicts.

State writes must be atomic:

```text
write state.json.tmp
fsync
rename state.json.tmp → state.json
```

For MVP, one JSON state file is acceptable.

Future scalability improvement:

```text
replace state.json with SQLite or BoltDB for large sync trees
```

---

## 14. State Update Rules

Sync state must be updated only after a successful, verified operation.

Do not update state based only on intended metadata.

After upload:

1. upload local file to remote temp path;
2. move remote temp path to final path;
3. perform `PROPFIND` for the final remote path;
4. read actual remote `ETag`, `getlastmodified`, and `getcontentlength`;
5. read local metadata again;
6. update sync state with confirmed local and remote metadata.

After download:

1. download remote file to local temp path;
2. verify size;
3. rename temp file to final local path;
4. set local mtime to remote mtime with `os.Chtimes`;
5. read local metadata again;
6. use remote metadata from the fresh remote entry, GET response headers, or fallback `PROPFIND`;
7. update sync state with confirmed metadata.

State must not be updated if:

```text
upload failed
download failed
remote MOVE failed
local rename failed
local chmod/chtime failed
verification failed
remote metadata refresh failed after upload
point-refresh failed before upload/delete
```

Recommended helper:

```go
func RefreshEntryState(ctx context.Context, path string) error {
    localEntry := scanLocalPath(path)
    remoteEntry := propfindRemotePath(path)
    state.Update(path, localEntry, remoteEntry)
    return state.Save()
}
```

---

## 15. Comparison Logic

Every comparison uses three inputs:

```text
previous sync state
current local tree
current remote tree
```

Do not compare only current local timestamps against current remote timestamps.

For each scoped path:

```go
localChanged  := local differs from previous local state
remoteChanged := remote differs from previous remote state
localDeleted  := existed in state but missing locally
remoteDeleted := existed in state but missing remotely
```

Definition of local change:

```text
local file appeared
OR local file disappeared
OR local size changed
OR local modTime changed beyond tolerance
```

Definition of remote change:

```text
remote file appeared inside tracked scope
OR remote file disappeared from tracked scope
OR remote ETag changed
OR remote size changed
OR remote modTime changed beyond tolerance
```

Time normalization:

```go
localTime := local.ModTime.UTC().Truncate(time.Second)
remoteTime := remote.ModTime.UTC().Truncate(time.Second)
```

Treat timestamps as equal if the difference is within 30 seconds.

Prefer remote `ETag` when available.

Remote changed if:

```text
ETag changed
OR size changed
OR modTime changed beyond tolerance
```

Local changed if:

```text
size changed
OR modTime changed beyond tolerance
```

Decision table:

| Condition                                  | Action             |
| ------------------------------------------ | ------------------ |
| localChanged && !remoteChanged             | upload             |
| remoteChanged && !localChanged             | download           |
| localChanged && remoteChanged              | conflict           |
| localDeleted && !remoteChanged             | move/delete remote |
| remoteDeleted && !localChanged             | move/delete local  |
| localDeleted && remoteChanged              | conflict           |
| remoteDeleted && localChanged              | conflict           |
| local exists && remote missing && no state | upload             |
| remote exists && local missing && no state | ignore by default  |
| both unchanged                             | skip               |

Remote-only untracked files must not create download or delete actions unless `--import-remote=true`.

---

## 16. Initial Sync Strategy

First run means there is no previous sync state.

Default first-run behavior:

| Condition                                          | Action                     |
| -------------------------------------------------- | -------------------------- |
| only local exists                                  | upload                     |
| only remote exists                                 | ignore                     |
| local and remote exist, same type and same size     | mark as synced             |
| local and remote exist, differ                     | conflict / skip by default |

Do not use automatic “newer wins” on first run by default.

Reason: without previous state, local and remote mtimes may legitimately differ because of provider import/export behavior, filesystem restores, or previous sync clients. First-run adoption should avoid false conflicts when the path, entry type, and size match.

Default:

```bash
--init-strategy=skip-conflicts
```

Optional first-run strategies:

```bash
--init-strategy=skip-conflicts
--init-strategy=local-wins
--init-strategy=remote-wins
--init-strategy=newer-wins
```

Migration note:

If local and remote folders were populated independently before `ysync`, the default `skip-conflicts` strategy may produce many conflicts because modTime values may not match.

For migration of an existing folder, recommended flow:

```bash
ysync -local ~/SyncFolder -remote / --dry-run
ysync -local ~/SyncFolder -remote / --dry-run --init-strategy=newer-wins
```

Then choose one of:

```bash
--init-strategy=newer-wins
--init-strategy=local-wins
--init-strategy=remote-wins
```

only after reviewing dry-run output.

Recommended first command:

```bash
ysync -local ~/SyncFolder -remote / --dry-run
```

---

## 17. WebDAV MOVE Requirements

`webdav.go` must explicitly set the WebDAV `Overwrite` header for every `MOVE`.

### Upload finalization

When moving remote temp file to final path:

```text
MOVE remote-temp → remote-final
Overwrite: T
```

Example:

```http
MOVE //docs/a.txt.ysync-uploading
Destination: //docs/a.txt
Overwrite: T
```

This is required because the final destination may already exist when updating an existing file.

### Remote conflict copy

When moving remote original to remote conflict copy:

```text
MOVE remote-original → remote-conflict-copy
Overwrite: F
```

Example:

```http
MOVE //docs/a.txt
Destination: //docs/a.txt.conflict-remote-20260628T120000Z
Overwrite: F
```

If the conflict destination already exists, generate a new unique conflict name and retry.

Example retry names:

```text
a.txt.conflict-remote-20260628T120000Z
a.txt.conflict-remote-20260628T120000Z-1
a.txt.conflict-remote-20260628T120000Z-2
```

### Remote trash move

When moving tracked remote file to trash:

```text
MOVE remote-original → remote-trash-path
Overwrite: F
```

If the trash destination already exists, generate a unique name.

---

## 18. Upload

Use upload when:

```text
file exists locally
AND file is inside sync scope
AND local version is changed
AND remote did not change after required point-refresh
AND no conflict is detected
```

Upload process:

1. perform required point-refresh for the remote path;
2. if point-refresh fails, mark retryable error and cancel upload;
3. if remote changed since state, mark conflict and cancel upload;
4. upload local file to remote temp path;
5. verify upload when possible;
6. move remote temp path to final path using `Overwrite: T`;
7. refresh confirmed metadata;
8. update sync state.

Remote temp path:

```text
<filename>.ysync-uploading
```

Finalization:

```text
PUT local → remote temp
MOVE remote temp → remote final with Overwrite: T
```

Upload resume is not required.

If upload fails:

* retry from scratch;
* max attempts: 3;
* exponential backoff: 1s → 2s → 4s;
* mark state as `error` if all retries fail.

No local `.ysync-uploading` file is created in the MVP.

Active upload tracking is handled by:

```text
scheduler active-path map
one active operation per path
state status = pending_upload
```

---

## 19. Download

Use download when:

```text
file is tracked in state
AND remote version changed
AND local did not change
```

Or when:

```text
--import-remote is enabled
AND file exists only remotely
```

Download process:

1. use fresh remote metadata from current scan or point-level metadata;
2. download to local temp file;
3. resume with HTTP Range if temp file exists;
4. verify size;
5. atomic rename to final path;
6. set local mtime to remote mtime using `os.Chtimes`;
7. refresh confirmed metadata;
8. update sync state.

Local temp path:

```text
<filename>.ysync-downloading
```

Finalization:

```go
os.Rename(tmpPath, finalPath)
os.Chtimes(finalPath, time.Now(), remoteModTime)
```

After download, remote metadata can be taken from:

* the fresh remote entry used to start download;
* GET response headers;
* fallback `PROPFIND` if required metadata is missing.

---

## 20. Conflict Resolution

Conflict occurs when both local and remote changed since the last synced state.

Default conflict strategy:

```bash
--conflict=skip
```

Default behavior:

* do not overwrite either side;
* log conflict;
* notify user;
* mark path as `conflict` in state;
* continue processing other files.

Supported strategies:

```bash
--conflict=skip
--conflict=local
--conflict=remote
```

### skip

Leave both local and remote versions untouched.

### local

Local wins.

Correct order:

```text
1. MOVE remote original → remote conflict copy with Overwrite: F
2. PUT local file → remote temp path
3. MOVE remote temp path → remote original with Overwrite: T
4. refresh confirmed metadata
5. update sync state
```

Remote conflict name:

```text
<filename>.conflict-remote-<UTC-timestamp>
```

If the remote conflict destination already exists, generate a new unique conflict name and retry.

Important: the remote conflict copy must be created before uploading the local file to the original path.

### remote

Remote wins.

Correct order:

```text
1. MOVE local original → local conflict copy
2. DOWNLOAD remote file → local temp path
3. RENAME local temp path → local original
4. set local mtime to remote mtime
5. refresh confirmed metadata
6. update sync state
```

Local conflict name:

```text
<filename>.conflict-local-<UTC-timestamp>
```

If the local conflict destination already exists, generate a new unique conflict name and retry.

Conflict copy files are ignored by default:

```text
*.conflict-local-*
*.conflict-remote-*
*.ysync-conflict-*
```

Notification:

```go
exec.Command(
    "osascript",
    "-e",
    `display notification "File changed both locally and in Yandex Disk" with title "ysync conflict"`,
).Run()
```

Notification is best-effort.

Sync must not fail if notification fails.

---

## 21. Delete Sync

Delete sync applies only to tracked files.

Remote-only untracked files must never be deleted.

Default delete behavior:

```bash
--delete=true
--delete-mode=trash
```

This is an intentional safety default.

Delete sync is enabled by default only for tracked files and only in trash mode.

This is considered conservative because:

* remote-only untracked files are never deleted;
* delete-vs-modify becomes conflict;
* point-refresh is required before remote delete/trash;
* hard delete is never default.

### Local delete

When a tracked file is deleted locally:

1. perform required point-refresh for the remote path;
2. if point-refresh fails, mark retryable error and cancel remote delete/trash;
3. if remote did not change since previous state, move remote file to remote trash;
4. if remote changed since previous state, mark conflict and do not delete remote.

### Remote delete

When a tracked file is deleted remotely:

1. compare local metadata with previous state;
2. if local did not change since previous state, move local file to local trash;
3. if local changed since previous state, mark conflict and do not delete local.

Delete decision table:

| Condition                                           | Action                       |
| --------------------------------------------------- | ---------------------------- |
| tracked file deleted locally, remote unchanged      | move/delete remote           |
| tracked file deleted remotely, local unchanged      | move/delete local            |
| tracked file deleted locally, remote changed        | conflict                     |
| tracked file deleted remotely, local changed        | conflict                     |
| point-refresh before remote delete failed           | retryable error, no mutation |
| remote-only untracked file exists                   | ignore                       |
| local-only untracked file deleted before first sync | ignore                       |

Supported delete modes:

```bash
--delete-mode=trash
--delete-mode=hard
```

Default:

```bash
--delete-mode=trash
```

For local delete in trash mode:

```text
move file to ~/.ysync/trash/<timestamp>/<path>
```

For remote delete in trash mode:

```text
MOVE remote file to /ysync-trash/<timestamp>/<path>
```

Remote trash path must be outside the configured sync root.

Example:

```text
remote root:  /
remote trash: /ysync-trash
```

Do not use:

```text
//.ysync-trash
```

Hard delete must be explicit.

Directory deletion:

* process files before directories;
* delete directories only when empty or explicitly confirmed by state;
* avoid deleting large directory trees unless all children are tracked.

---

## 22. Rename Handling

MVP does not implement true rename detection.

Default behavior:

```text
rename = delete old path + create new path
```

With default settings:

```text
old tracked path → remote moved to trash
new local path → uploaded as new file
```

Cost warning:

Renaming a large file causes a full re-upload in the MVP.

The service does not detect that the old file and the new file have identical contents.

Future improvement:

```text
detect rename by matching size + ETag/hash
perform WebDAV MOVE instead of delete+upload
```

---

## 23. Local Temp Cleanup

Local temp files may remain after interrupted downloads or crashes.

Stale local temp files:

```text
*.ysync-downloading
*.ysync.tmp
*.ysync.part
```

Startup handling:

| Temp file            | Action                                                 |
| -------------------- | ------------------------------------------------------ |
| `.ysync-downloading` | resume download if matching tracked remote file exists |
| `.ysync.tmp`         | remove if older than safety TTL                        |
| `.ysync.part`        | remove if older than safety TTL                        |

Default stale local temp TTL:

```bash
--temp-ttl 24h
```

Important:

`*.ysync-uploading` is not a local temp file in the MVP and is not handled by local startup cleanup.

---

## 24. Remote Temp Cleanup

Remote temp upload files may remain if the process dies after `PUT` but before `MOVE`.

Remote temp pattern:

```text
*.ysync-uploading
```

Normal remote sync scans must ignore `*.ysync-uploading`.

Remote temp cleanup is a separate maintenance task and must explicitly look for this pattern, bypassing the normal ignore filter for service temp files.

Default remote temp TTL:

```bash
--remote-temp-ttl 24h
```

Cleanup rule:

```text
if remote file matches *.ysync-uploading
AND age > remote temp TTL
AND no active upload job owns this path
THEN move it to remote trash
```

Default cleanup action:

```text
MOVE stale remote temp → /ysync-trash/temp/<timestamp>/<path>
Overwrite: F
```

If destination already exists, generate a unique name.

Remote temp cleanup must:

* never update normal sync state;
* never import temp files even when `--import-remote=true`;
* invalidate remote cache after cleanup;
* log every cleanup action;
* skip cleanup if remote metadata is incomplete or stale age cannot be determined.

If remote temp cleanup fails:

```text
log warning
do not affect normal sync jobs
retry later
```

---

## 25. Fault Tolerance

Requirements:

* all transfers are retryable;
* failed jobs must not corrupt local or remote files;
* temp files must be ignored before debounce;
* service restart must not treat temp files as user files;
* partial downloads can resume;
* partial uploads are discarded and retried from scratch;
* incomplete remote scans must not trigger upload/delete decisions;
* failed point-refresh must not trigger upload/delete decisions.

On startup:

```text
1. load sync state
2. scan local tree
3. scan remote tree
4. clean or resume stale local temp files
5. run remote temp cleanup maintenance
6. ignore service temp files
7. compute sync plan
8. execute only safe actions
```

If remote scan fails during startup:

```text
do not execute sync plan
keep service running
retry later
```

---

## 26. Async Operations and Concurrency

Uploads, downloads, delete/trash operations, and cleanup operations run concurrently using goroutines.

Concurrency must be limited.

Default:

```bash
--concurrency 3
```

Requirements:

* max 3 concurrent user-facing sync operations total by default;
* one active operation per path;
* scheduler must deduplicate pending jobs for the same path;
* cleanup jobs must not conflict with active upload/download/delete jobs;
* graceful shutdown on `SIGTERM` / `SIGINT`;
* in-flight transfers may finish during shutdown;
* no new transfers should start after shutdown begins.

---

## 27. Dry Run Mode

Flag:

```bash
--dry-run
```

Dry run must not mutate local files, remote files, or sync state.

It should:

* scan local files;
* scan remote files;
* load sync state;
* compute sync plan;
* print actions;
* exit without upload/download/delete/state update.

Output example:

```text
[upload]   docs/a.txt        local new file
[download] docs/b.txt        tracked remote changed
[conflict] docs/c.txt        changed locally and remotely
[trash]    docs/d.txt        tracked file deleted locally, remote unchanged
[ignore]   remote/e.txt      remote-only untracked file
[cleanup]  docs/a.txt.ysync-uploading stale remote temp, would move to trash
[skip]     docs/f.txt        unchanged
```

Dry-run must explicitly show ignored remote-only files only when verbose mode is enabled.

Dry-run should show remote temp cleanup actions when verbose mode is enabled or when `--show-cleanup=true`.

Default dry-run output should focus on scoped files.

---

## 28. Bandwidth Limit

Optional bandwidth limit:

```bash
--bandwidth 0
```

Default:

```text
0 = unlimited
```

If set, apply transfer throttling.

Implementation idea:

```go
rate.NewLimiter(rate.Limit(maxBytesPerSecond), burst)
```

Bandwidth limit applies per transfer.

Concurrency still limits total number of active transfers.

---

## 29. macOS Service

The service should run as a user LaunchAgent.

Plist path:

```text
~/Library/LaunchAgents/com.ysync.service.plist
```

Example:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.ysync.service</string>

    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/ysync</string>
        <string>-local</string>
        <string>/Users/username/SyncFolder</string>
        <string>-remote</string>
        <string>/</string>
        <string>-conflict</string>
        <string>skip</string>
        <string>-delete-mode</string>
        <string>trash</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
        <key>YANDEX_USER</key>
        <string>login@yandex.ru</string>
    </dict>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>

    <key>ThrottleInterval</key>
    <integer>10</integer>

    <key>StandardOutPath</key>
    <string>/Users/username/Library/Logs/ysync/stdout.log</string>

    <key>StandardErrorPath</key>
    <string>/Users/username/Library/Logs/ysync/stderr.log</string>
</dict>
</plist>
```

Activation:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.ysync.service.plist
```

Unload:

```bash
launchctl bootout gui/$(id -u)/com.ysync.service
```

Bad credentials or invalid configuration should fail clearly and avoid infinite crash-loop behavior.

---

## 30. Credentials and Yandex Auth

Recommended credential source:

```text
macOS Keychain
```

Environment variables are allowed for development.

Avoid passing secrets through CLI args.

Recommended Keychain commands:

```bash
security add-generic-password -s "ysync" -a "yandex_user" -w "login@yandex.ru"
security add-generic-password -s "ysync" -a "yandex_pass" -w "app_secret"
```

Retrieve in Go:

```go
exec.Command(
    "security",
    "find-generic-password",
    "-s", "ysync",
    "-a", "yandex_user",
    "-w",
)
```

Supported auth sources, in priority order:

```text
1. explicit --keychain-profile
2. macOS Keychain default profile
3. environment variables
4. CLI flags, discouraged
```

Environment variables:

```bash
YANDEX_USER
YANDEX_PASS
YANDEX_TOKEN
```

Yandex WebDAV auth modes to support:

```text
1. Basic Auth with Yandex login + app password
2. Basic Auth with Yandex login + OAuth token used as password, if verified
3. Bearer token only if verified against Yandex WebDAV
```

Implementation requirement:

`webdav.go` must explicitly define how `-token` is sent.

Do not implement ambiguous token behavior.

Before implementation is considered complete, add a manual verification command or integration test for the selected auth mode.

Example expected Basic Auth behavior:

```text
username = Yandex login
password = app password or verified OAuth token mode
```

---

## 31. Logging

Use structured logs.

Example:

```text
2026-06-28T10:00:00Z [INFO] upload completed path=docs/a.txt size=1.2MB
2026-06-28T10:01:00Z [WARN] conflict path=docs/b.txt local=... remote=...
2026-06-28T10:02:00Z [ERROR] upload failed path=docs/c.txt error=...
```

Levels:

```text
DEBUG
INFO
WARN
ERROR
```

Default output:

```text
stdout/stderr
```

For LaunchAgent, logs should go to:

```text
~/Library/Logs/ysync/stdout.log
~/Library/Logs/ysync/stderr.log
```

Optional:

```bash
--log-file ~/Library/Logs/ysync/ysync.log
```

Log rotation is optional for MVP.

---

## 32. CLI Interface

```text
Usage: ysync [flags]

Required:
  -local string              Local folder to sync
  -remote string             Remote Yandex Disk folder, default "/"

Auth:
  -user string               Yandex login, or $YANDEX_USER
  -pass string               Yandex app password, or $YANDEX_PASS
  -token string              Yandex OAuth token, or $YANDEX_TOKEN
  -keychain-profile string   Read credentials from macOS Keychain

Sync:
  -url string                WebDAV base URL, default "https://webdav.yandex.ru"
  -interval duration         Remote scan/cache TTL, default 5m
  -debounce duration         File debounce delay, default 1m
  -local-scan-interval dur   Fallback local polling interval, default 1m
  -remote-workers int        Workers for recursive Depth: 1 crawl, default 4
  -concurrency int           Max concurrent operations, default 3
  -conflict string           Conflict strategy: skip/local/remote, default skip
  -delete                    Enable delete sync for tracked files, default true
  -delete-mode string        trash/hard, default trash
  -import-remote             Import remote-only untracked files, default false
  -init-strategy string      skip-conflicts/local-wins/remote-wins/newer-wins

Filters:
  -ignore-file string        Path to ignore file
  -include-hidden            Include hidden files, default false

Safety:
  -dry-run                   Print sync plan, do not modify anything
  -temp-ttl duration         Stale local temp file TTL, default 24h
  -remote-temp-ttl duration  Stale remote temp file TTL, default 24h
  -show-cleanup              Show cleanup actions in dry-run output
  -bandwidth int             Max bytes/sec per transfer, default 0 unlimited
  -verbose                   Enable debug logging
  -version                   Print version
```

---

## 33. Go File Structure

```text
main.go
ysync/
├── config.go          # CLI flags, env vars, defaults
├── syncer.go          # Top-level orchestrator
├── watcher.go         # fsnotify recursive watcher
├── debounce.go        # per-path debounce and stable-file checks
├── local.go           # local tree scanner
├── webdav.go          # PROPFIND, GET, PUT, MOVE, DELETE
├── remote.go          # remote tree loading and normalization
├── cache.go           # remote tree TTL cache
├── state.go           # sync state persistence
├── comparator.go      # state-aware diff generation
├── planner.go         # sync plan creation
├── scheduler.go       # transfer queue and concurrency
├── transfer.go        # upload/download implementation
├── conflict.go        # conflict strategies
├── delete.go          # delete handling
├── cleanup.go         # local and remote temp cleanup
├── ignore.go          # ignore patterns
├── notify.go          # macOS notifications
├── log.go             # structured logging
└── errors.go          # common errors
```

---

## 34. Implementation Requirements by Module

### watcher.go

Must handle:

```text
recursive watch setup
new directory watch registration
removed directory watch cleanup
ignored path early drop
EMFILE / ENFILE detection
degraded polling fallback
shutdown cleanup
```

### ignore.go

Must run before debounce.

Default ignore list must include:

```text
._*
.DS_Store
.localized
*.ysync.tmp
*.ysync.part
*.ysync-downloading
*.ysync-uploading
*.conflict-local-*
*.conflict-remote-*
*.ysync-conflict-*
```

### debounce.go

Must use dual-dimensional stability:

```text
size unchanged
AND modTime unchanged
```

If file stat/read fails temporarily, keep the path pending and retry.

### webdav.go

Must implement:

```text
PROPFIND
GET
PUT
MOVE
DELETE
Basic Auth
verified token auth mode
MOVE Overwrite header
Destination header construction
HTTP Range for downloads
```

MOVE requirements:

```text
temp → final: Overwrite: T
original → conflict copy: Overwrite: F
original → trash path: Overwrite: F
stale remote temp → trash path: Overwrite: F
```

### remote.go

Must implement:

```text
recursive Depth: 1 remote crawl
bounded worker pool
retry/backoff
context cancellation
per-path metadata refresh
```

### comparator.go

Must compare:

```text
previous state
current local tree
current remote tree
```

Must not use current local vs current remote alone.

Remote-only untracked paths must be ignored unless `--import-remote=true`.

Service-generated remote temp paths must never be imported.

### state.go

Must persist:

```text
local size
local modTime
remote size
remote modTime
remote ETag
status
last synced timestamp
```

State updates must be atomic.

### scheduler.go

Must guarantee:

```text
one active operation per path
concurrency limit
conflicts do not block unrelated files
no new jobs after shutdown begins
cleanup jobs do not conflict with active sync jobs
```

### transfer.go

Must guarantee:

```text
download to temp file
upload to remote temp path
atomic rename/MOVE after success
state refresh after confirmed success
point-refresh before upload
no state update after failed transfer
```

### delete.go

Must guarantee:

```text
delete applies only to tracked files
remote-only untracked files are never deleted
default delete mode is trash
point-refresh before remote delete/trash
delete-vs-modify becomes conflict
remote trash is outside sync root
```

### cleanup.go

Must guarantee:

```text
local stale temp cleanup
remote stale temp cleanup
remote temp files ignored by normal sync
remote temp cleanup bypasses normal ignore only for service temp patterns
cleanup never updates normal sync state
cleanup invalidates remote cache after remote moves
cleanup logs all actions
```

---

## 35. MVP Safety Defaults

Default behavior must be conservative:

```text
remote-only untracked files → ignore
service-generated remote temp files → ignore in sync scan
stale remote temp files → move to remote trash after TTL
conflicts → skip
tracked local delete → remote trash
tracked remote delete → local trash
untracked remote file → never delete
first-run differing files → conflict
PROPFIND Depth: infinity → not authoritative for Yandex Disk
PROPFIND Depth: 1 recursive crawl → required remote scan mode
ignored files → dropped before debounce
watcher failure → degraded polling mode
upload resume → not supported
download resume → supported when possible
MOVE temp → final → Overwrite: T
MOVE to conflict/trash → Overwrite: F
point-refresh before upload/delete → required
point-refresh failure → no mutation, retry later
state update → only after confirmed metadata refresh
secrets in CLI args → discouraged
```

Recommended first usage:

```bash
ysync \
  -local ~/SyncFolder \
  -remote / \
  --dry-run
```

Then run as a service only after dry-run output looks correct.

---

## 36. Suggested Implementation Order

Recommended implementation phases:

### Phase 1 — Core Data Model

* config parsing;
* local scanner;
* remote metadata model;
* sync state model;
* atomic state save/load.

### Phase 2 — Comparator and Dry Run

* three-way comparison;
* sync scope rules;
* conflict detection;
* delete detection for tracked files;
* dry-run output.

This phase should work before any real file mutation.

### Phase 3 — WebDAV Client

* `PROPFIND`;
* `GET`;
* `PUT`;
* `MOVE`;
* `DELETE`;
* recursive `Depth: 1` remote crawling;
* `Overwrite` header behavior;
* auth verification;
* retries and context cancellation.

### Phase 4 — Transfers

* safe upload via remote temp path;
* safe download via local temp path;
* download resume;
* required point-refresh before upload;
* metadata verification;
* state refresh after success.

### Phase 5 — Delete / Trash

* tracked local delete → remote trash;
* tracked remote delete → local trash;
* required point-refresh before remote trash;
* hard delete only as explicit opt-in;
* remote trash outside sync root.

### Phase 6 — Cleanup

* local stale temp cleanup;
* remote stale temp cleanup;
* remote `.ysync-uploading` ignore behavior;
* cleanup dry-run output;
* cache invalidation after remote cleanup.

### Phase 7 — Watcher and Debouncer

* recursive fsnotify watching;
* early ignore filtering;
* stable file detection;
* EMFILE/ENFILE degraded polling fallback.

### Phase 8 — Scheduler

* queues;
* concurrency limits;
* one operation per path;
* graceful shutdown;
* cleanup job coordination.

### Phase 9 — macOS Integration

* LaunchAgent plist;
* Keychain credential retrieval;
* notifications;
* logs.

---

## 37. Testing Requirements

Minimum test coverage:

### Unit Tests

* ignore pattern matching;
* conflict-file ignore patterns;
* `.ysync-uploading` ignore behavior;
* local path normalization;
* remote path normalization;
* timestamp comparison with tolerance;
* state load/save;
* atomic state write;
* comparator decision table;
* conflict detection;
* delete detection;
* dry-run plan generation.

### WebDAV Client Tests

* `MOVE` temp to final uses `Overwrite: T`;
* `MOVE` original to conflict copy uses `Overwrite: F`;
* `MOVE` original to trash uses `Overwrite: F`;
* `MOVE` stale remote temp to trash uses `Overwrite: F`;
* `Destination` header is encoded correctly;
* Basic Auth headers are generated correctly;
* selected token auth mode is verified.

### Integration Tests

* local-only file upload plan;
* tracked remote change download plan;
* remote-only untracked file ignored;
* remote `.ysync-uploading` ignored during normal sync;
* stale remote `.ysync-uploading` moved to trash by cleanup;
* `--import-remote=true` does not import service temp files;
* both local and remote changed conflict;
* local delete moves tracked remote file to trash;
* remote delete moves tracked local file to trash;
* local delete + remote modify becomes conflict;
* remote scan failure does not mutate state;
* point-refresh failure does not upload or delete;
* failed upload does not update state;
* successful upload refreshes remote metadata before state update;
* point-refresh prevents upload over fresh remote change;
* conflict name collision generates a unique conflict name.

### Manual Tests

* first run dry-run;
* migration dry-run with `--init-strategy=newer-wins`;
* large file written slowly;
* interrupted download resume;
* interrupted upload retry from scratch;
* interrupted upload leaving remote `.ysync-uploading`;
* remote temp cleanup after TTL;
* LaunchAgent start/stop;
* bad credentials do not create tight crash loop;
* Keychain credential retrieval;
* recursive watcher with many files/directories;
* degraded polling mode.

---

## 38. Final Product Behavior Summary

By default, `ysync` behaves as a conservative local-scoped sync daemon:

```text
local new file                    → upload
local changed tracked file        → upload
remote changed tracked file       → download
remote-only unknown file          → ignore
remote service temp file          → ignore in normal sync
stale remote service temp file    → move to remote trash after TTL
both sides changed                → conflict, skip
tracked local delete              → remote trash
tracked remote delete             → local trash
delete-vs-modify                  → conflict
rename                            → delete old + upload new
watcher failure                   → polling fallback
remote scan failure               → no mutation, retry later
point-refresh failure             → no mutation, retry later
```

This makes the MVP safe for personal use while keeping the implementation realistic and maintainable.
