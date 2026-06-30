# ysync UI Spec - Local Sync Dashboard

## 1. Overview

`ysync` should provide an optional local web UI for inspecting sync state, reviewing planned operations, resolving conflicts, and performing explicit user-initiated sync actions.

The UI is a local control panel, not a replacement for Yandex Disk's web interface.

Default access:

```text
http://127.0.0.1:<port>/
```

The UI must bind to `127.0.0.1` by default and must not listen on public interfaces unless explicitly requested.

Recommended frontend approach:

```text
Go net/http + server-rendered HTML templates + htmx
```

Reason: the UI is operational and state-heavy, but not graphically complex. htmx keeps the implementation simple, avoids a frontend build pipeline, and fits the existing Go service.

---

## 2. Goals

The UI must:

* show current sync status at a glance;
* show files that need attention;
* show a lazily loaded local/remote/state tree;
* show dry-run plans before mutation;
* allow explicit per-path sync actions;
* allow conflict review and resolution;
* show recent operations and errors;
* avoid accidental data loss;
* work fully on localhost without external services;
* keep secrets out of the browser and URLs.

---

## 3. Non-Goals

The UI must not:

* expose the service to the LAN by default;
* become a full Yandex Disk file manager;
* stream previews of arbitrary file contents in MVP;
* require React, Vue, Vite, npm, or a frontend build pipeline in MVP;
* perform destructive operations without confirmation;
* enable hard delete by default;
* bypass the same sync safety checks used by CLI execution;
* store credentials in browser storage.

---

## 4. UX Model

The UI should be a dashboard for sync decisions.

Primary screens:

```text
Dashboard
Plan
Conflicts
Tree
Operations Log
Settings
```

Default landing screen:

```text
Dashboard
```

The dashboard should prioritize actionable state:

* conflicts;
* failed operations;
* files planned for upload;
* files planned for download;
* tracked deletes;
* ignored remote-only files, only as secondary information;
* current scan freshness.

The full tree is secondary and should be lazy-loaded.

---

## 5. Status Model

Every displayed path should have a derived UI status.

Core statuses:

```text
synced
mark-synced
upload
download
conflict
trash-remote
trash-local
delete-remote
delete-local
remote-only-ignored
local-only
ignored
error
unknown
```

Visual indicators:

| Status              | Indicator       |
| ------------------- | --------------- |
| synced              | green check     |
| mark-synced         | green check     |
| upload              | upward arrow    |
| download            | downward arrow  |
| conflict            | warning icon    |
| delete/trash        | trash icon      |
| ignored             | muted eye-off   |
| error               | red alert       |

The UI should not depend on color alone. Icons and text labels are required.

`mark-synced` appears both as a row status and as an action name. A row gets `mark-synced` status when the comparator says it can be safely adopted into state, and the UI may expose the matching `mark synced` action for that row.

---

## 6. Tree View

The tree view should show remote, local, and state-aware sync status.

Required behavior:

* lazy-load folder children on expand;
* show file/folder name, status, size, modified time, and action menu;
* support search by path substring;
* support filters by status;
* avoid loading the full remote tree on initial page load;
* preserve expansion state during htmx swaps where practical.

Tree row fields:

```go
type UITreeNode struct {
    Path          string
    Name          string
    IsDir         bool
    Status        string
    LocalExists   bool
    RemoteExists  bool
    Tracked       bool
    LocalSize     int64
    RemoteSize    int64
    LocalModTime  time.Time
    RemoteModTime time.Time
    RemoteETag    string
    ChildrenURL   string
}
```

Remote-only untracked files should be visible in the tree, but clearly marked as ignored by default.

---

## 7. Plan View

The plan view should render the same operations produced by the comparator.

Required filters:

```text
all
uploads
downloads
conflicts
deletes
mark-synced
ignored
errors
```

Plan rows:

```go
type UIPlanRow struct {
    Action string
    Path   string
    Reason string
    Size   int64
}
```

The plan view must support refresh.

Refresh behavior:

```text
scan local
scan remote if remote cache is stale or user forces refresh
load state
compare
render plan
```

For expensive remote roots, the UI must show scan progress or at least a clear "scanning" state.

---

## 8. Conflict View

The conflict view should show only paths needing user decision.

For each conflict, show:

* path;
* local exists;
* remote exists;
* local size;
* remote size;
* local modified time;
* remote modified time;
* remote ETag if available;
* current state status;
* available resolution actions.

MVP resolution actions:

```text
keep local / upload
keep remote / download
mark synced, only when safe
```

`mark synced` is safe only when:

```text
local exists
remote exists
same entry type
same size
```

The UI may later add optional hash verification, but MVP should not download large remote files automatically for every conflict.

---

## 9. Actions

All mutating actions must be POST requests.

Actions must reuse existing sync executor safety checks.

MVP actions:

```text
POST /actions/refresh
POST /actions/run-plan
POST /actions/path/mark-synced
POST /actions/path/upload
POST /actions/path/download
POST /actions/path/trash-remote
POST /actions/path/trash-local
```

Non-MVP actions:

```text
POST /actions/path/hard-delete-remote
POST /actions/path/hard-delete-local
POST /actions/import-remote-subtree
POST /actions/rescan-remote-subtree
```

Dangerous actions:

* hard delete;
* trash local;
* trash remote;
* run full plan;
* import remote subtree.

Dangerous actions must require confirmation in the UI and a server-side one-time confirmation token.

Confirmation tokens:

* are generated by `GET /partials/confirm`;
* are bound to exact `action + path`;
* have a short TTL;
* are single-use;
* are stored server-side;
* are embedded into the confirmation modal as a hidden form field;
* must be validated by the POST handler before the action executes.

---

## 10. htmx Architecture

The UI should use htmx for partial page updates.

Use normal server-rendered templates for:

```text
layout
dashboard
plan table
tree rows
conflict rows
operation log
modal dialogs
toast/status messages
```

Example interaction:

```html
<button
  hx-post="/actions/refresh"
  hx-target="#main"
  hx-swap="innerHTML"
  hx-indicator="#refresh-indicator"
  hx-disabled-elt="this">
  <span class="label">Refresh</span>
  <span id="refresh-indicator" class="htmx-indicator">Refreshing...</span>
</button>
```

Templates must include an in-progress indicator for slow requests.

Required for:

```text
POST /actions/refresh
POST /actions/run-plan
remote recursive scan
full plan execution
destructive path actions
```

In-progress behavior:

* disable the initiating button while the request is active;
* show an `htmx-indicator` spinner or short text label;
* avoid duplicate submissions from repeated clicks;
* show completion or error state after the response swaps in.

Use `hx-disabled-elt="this"` where supported by the chosen htmx version. If unavailable, templates must provide equivalent behavior with the smallest possible local JavaScript.

Tree expansion:

```html
<button
  hx-get="/tree?path=Documents"
  hx-target="#children-Documents"
  hx-swap="innerHTML">
  Expand
</button>
```

Action confirmation:

```html
<button
  hx-get="/partials/confirm?action=trash-remote&path=docs/a.txt"
  hx-target="#modal"
  hx-swap="innerHTML">
  Trash remote
</button>
```

`GET /partials/confirm` must generate a one-time server-side confirmation token bound to:

```text
action=trash-remote
path=docs/a.txt
ttl=<short duration>
```

The returned modal must include the token as a hidden form field:

```html
<form
  hx-post="/actions/path/trash-remote"
  hx-target="#main"
  hx-swap="innerHTML">
  <input type="hidden" name="path" value="docs/a.txt">
  <input type="hidden" name="confirm_token" value="{{.ConfirmToken}}">
  <button type="submit">Confirm</button>
</form>
```

Then the modal submits:

```text
POST /actions/path/trash-remote path=docs/a.txt confirm_token=<token>
```

The POST handler must reject missing, expired, reused, or action/path-mismatched confirmation tokens.

No client-side framework is required for MVP.

Small JavaScript is allowed only for:

* keyboard shortcuts;
* preserving tree expansion state;
* local-only UI niceties.

---

## 11. HTTP Routes

Page routes:

```text
GET /
GET /plan
GET /conflicts
GET /tree
GET /logs
GET /settings
```

Partial routes:

```text
GET /partials/dashboard
GET /partials/plan?filter=<filter>
GET /partials/conflicts
GET /partials/tree?path=<path>
GET /partials/path-details?path=<path>
GET /partials/confirm?action=<action>&path=<path>
```

Action routes:

```text
POST /actions/refresh
POST /actions/run-plan
POST /actions/path/mark-synced
POST /actions/path/upload
POST /actions/path/download
POST /actions/path/trash-remote
POST /actions/path/trash-local
```

API routes may be added later, but MVP should prefer HTML partial routes.

---

## 12. Security

The UI must be local-only by default.

Default bind address:

```text
127.0.0.1
```

Default port:

```text
8345
```

If port `8345` is already in use, the UI should fail with a clear error and suggest `-ui-port <port>`.

The UI must not silently choose another port unless the user explicitly passes `-ui-port 0`.

Session:

* the public entry point must be `http://127.0.0.1:8345`;
* set a local `HttpOnly` session cookie on the first valid localhost request;
* the first cookie-issuing request must pass the same Origin/Referer policy as all later requests;
* require the session cookie for subsequent routes;
* do not store Yandex credentials in the browser;
* do not expose Basic Auth credentials to templates.

CSRF/CORS:

* reject non-local `Origin`;
* when `Origin` is absent, allow only direct top-level navigation with local `Host`;
* reject non-local `Referer` when `Referer` is present;
* do not make a bootstrap exception for the first session-cookie request;
* set `SameSite=Strict` for UI cookies;
* require POST for mutations;
* require confirmation token for dangerous actions;
* do not enable wildcard CORS.

Recommended launch output:

```text
ysync UI listening on http://127.0.0.1:8345
```

The session cookie value should not be logged.

---

## 13. State and Caching

The UI should use the same state file as the sync service.

The UI must avoid concurrent unsafe state writes.

Rules:

* use a narrow state-write mutex only around state load/modify/save sections;
* do not hold the state-write mutex during remote scans, local scans, or compare-only rendering;
* use a separate UI mutation mutex for explicit UI-initiated mutations;
* do not run two UI-initiated mutating actions concurrently;
* do not use the UI mutation mutex to block read-only dashboard, plan, tree, or log requests;
* background watcher/scheduler work must not make the UI permanently show "operation running";
* show "operation running" only for an active mutation that conflicts with the requested action;
* write state atomically through existing state save logic.

Remote cache:

* full account-root recursive scan can be expensive;
* cache remote tree in memory with timestamp;
* provide "force refresh";
* allow subtree refresh for expanded folders in a later version.

---

## 14. CLI Flags

New flags:

```text
-ui-addr string         UI bind address, default 127.0.0.1
-ui-port int            UI port, default 8345
-ui-open                Open browser after UI starts
-ui-no-session          Disable UI session cookie, only for development
```

`-ui-open` behavior:

* after the listener starts successfully, open the public UI URL;
* on macOS, use `exec.Command("open", url).Run()`;
* the URL must be `http://127.0.0.1:<port>`;
* browser opening is best-effort and must not stop the UI server if it fails.

Recommended usage:

```bash
ysync -local ~/Yandex.Disk.localized/Project -remote /Project -ui
```

Avoid using `-remote /` in examples. Account-root recursive scans are expensive and should be reserved for users who intentionally sync the whole Yandex Disk root.

The UI should be compatible with LaunchAgent mode, but MVP may start it manually.

---

## 15. Backend Structure

Suggested files:

```text
ui/server.go       # HTTP server, routes, auth middleware
ui/handlers.go     # page/partial/action handlers
ui/model.go        # view models and status derivation
ui/templates.go    # embedded templates
ui/actions.go      # wrappers around sync operations
ui/security.go     # session cookie, origin, confirmation helpers
```

Template files may be embedded later:

```text
ui/templates/*.html
ui/static/*.css
```

MVP can use `html/template` with embedded template strings if that keeps the implementation smaller.

---

## 16. Error Handling

Errors should be visible and actionable.

UI error display:

* show short error message near the affected action;
* include path when relevant;
* include retry button when safe;
* log full details to structured logger;
* never expose credentials.

Common errors:

```text
auth failed
remote rate limited
remote scan failed
local file disappeared
remote path changed before action
state root mismatch
operation already running
```

---

## 17. MVP Milestones

### MVP 1 - Read-only Dashboard

* `-ui` starts local server;
* localhost route protected by session cookie;
* dashboard page;
* plan page;
* conflict page;
* refresh button;
* htmx partial updates;
* no mutating actions.

### MVP 2 - Safe Per-Path Actions

* mark synced;
* upload one path;
* download one path;
* confirmation modal;
* operation log.

### MVP 3 - Tree View

* lazy tree expansion;
* status badges;
* filters;
* path details panel.

### MVP 4 - Destructive Actions

* trash remote;
* trash local;
* hard delete only behind explicit opt-in;
* server-side confirmation token.

---

## 18. Acceptance Criteria

Read-only UI:

* starts with `ysync -ui`;
* binds to `127.0.0.1`;
* serves the public entry point at `http://127.0.0.1:8345`;
* shows plan equivalent to CLI dry-run;
* shows conflicts and reasons;
* does not mutate files/state.

Action UI:

* all mutations use POST;
* dangerous actions require confirmation;
* operations reuse existing executor checks;
* state is saved atomically;
* concurrent mutations are rejected or queued safely.

Tree UI:

* folder expansion is lazy;
* remote-only ignored files are visible but not imported by default;
* synced files show green check;
* same-size first-run matches show `mark-synced`;
* NFC-equivalent paths are displayed once;
* filenames containing literal `%2F` are displayed as one filename, not as a folder.

Security:

* session cookie required after first localhost request;
* CORS disabled by default;
* localhost-only by default;
* credentials never rendered into HTML.
