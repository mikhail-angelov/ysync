# ysync

`ysync` is a macOS sync service for a local folder and Yandex Disk. It uses the Yandex Disk WebDAV API, watches local changes, syncs in the background, and provides a local web UI for reviewing plans, conflicts, and file status.

It is designed to be local-scoped: by default, it syncs files that are present locally or already tracked in sync state. Remote-only files are ignored unless you explicitly import them.

## Requirements

- macOS
- Go 1.21 or newer
- Yandex Disk with WebDAV access
- A Yandex app password

## Install

Install as a per-user macOS LaunchAgent from a GitHub release:

```bash
curl -fsSL https://raw.githubusercontent.com/mikhail-angelov/ysync/main/scripts/install.sh \
  | sh -s -- --local-folder "$HOME/SyncFolder"
```

To install from a local checkout instead:

```bash
git clone https://github.com/mikhail-angelov/ysync.git
cd ysync
./scripts/install.sh --source local --local-folder "$HOME/SyncFolder"
```

The installer writes `ysync` to `~/.local/bin/ysync`, writes the LaunchAgent plist to `~/Library/LaunchAgents/com.ysync.service.plist`, and starts the service for the current user.

Credentials can be imported into the macOS Keychain during install:

```bash
YANDEX_USER="your@yandex.login" YANDEX_PASS="your-app-password" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/mikhail-angelov/ysync/main/scripts/install.sh)" -- \
    --local-folder "$HOME/SyncFolder"
```

When credentials are provided, the installer stores them under the `ysync` Keychain profile unless `--keychain-profile` is set.

To sync a specific remote folder:

```bash
curl -fsSL https://raw.githubusercontent.com/mikhail-angelov/ysync/main/scripts/install.sh \
  | sh -s -- --local-folder "$HOME/SyncFolder" --remote-folder /Documents
```

Use a specific release, local binary, or install location:

```bash
./scripts/install.sh --version v0.1.0 --local-folder "$HOME/SyncFolder"
./scripts/install.sh --source local --binary ./bin/ysync --local-folder "$HOME/SyncFolder"
./scripts/install.sh --install-dir "$HOME/bin" --local-folder "$HOME/SyncFolder"
```

Uninstall the LaunchAgent:

```bash
./scripts/install.sh --uninstall
./scripts/install.sh --uninstall --remove-binary
```

For development, you can still build with `make`:

```bash
make build
```

The development binary is written to `bin/ysync`.

## Authentication

`ysync` resolves credentials in this order:

1. `-keychain-profile <name>` if provided
2. explicit command-line values such as `-user` or `-pass`
3. the default macOS Keychain profile named `ysync`
4. environment variables for any values still missing

Recommended Keychain setup:

```bash
security add-generic-password -U -s ysync -a yandex_user -w "your@yandex.login"
security add-generic-password -U -s ysync -a yandex_pass -w "your-app-password"
```

Environment variables are also supported:

```bash
export YANDEX_USER="your@yandex.login"
export YANDEX_PASS="your-app-password"
```

## First Run

Before starting the background service, run a dry run to inspect the plan:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --dry-run --verbose
```

If the local and remote folders already contain files, choose an initial sync strategy:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --dry-run --init-strategy skip-conflicts
bin/ysync -local "$HOME/SyncFolder" -remote / --dry-run --init-strategy newer-wins
```

Initial sync strategies:

| Strategy | Behavior |
| --- | --- |
| `skip-conflicts` | Upload local-only files and skip files that already exist on both sides with different metadata. This is the default. |
| `local-wins` | Upload the local version when both sides differ. |
| `remote-wins` | Download the remote version when both sides differ. |
| `newer-wins` | Use whichever file has the newer modification time. |

## Run Manually

Run the service in the foreground:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote /
```

Open the web UI:

```text
http://127.0.0.1:8787
```

Common manual commands:

```bash
# Inspect planned changes only
bin/ysync -local "$HOME/SyncFolder" -remote / --dry-run

# Sync a remote subfolder
bin/ysync -local "$HOME/SyncFolder" -remote /Documents

# Include remote-only files in the plan
bin/ysync -local "$HOME/SyncFolder" -remote / --import-remote

# Resolve conflicts by preferring local files
bin/ysync -local "$HOME/SyncFolder" -remote / --conflict local
```

## LaunchAgent Management

Start or restart the installed service:

```bash
launchctl kickstart -k "gui/$(id -u)/com.ysync.service"
```

Stop the service:

```bash
launchctl bootout "gui/$(id -u)/com.ysync.service"
```

View logs:

```bash
tail -f "$HOME/Library/Logs/ysync/stdout.log"
tail -f "$HOME/Library/Logs/ysync/stderr.log"
```

Regenerate the plist without installing:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --print-launch-agent
```

## Web UI

The web UI runs locally at `http://127.0.0.1:8787` by default.

It provides:

- Dashboard with current sync status
- Plan view for pending uploads, downloads, deletes, and conflicts
- Conflicts view with per-file resolution actions
- Tree view for local, remote, and tracked files
- Operations log for the current service session
- Settings page with the active configuration

Useful actions:

| Action | Meaning |
| --- | --- |
| `Sync` | Download a remote-only file or run a pending operation. |
| `Keep local` | Resolve a conflict by uploading the local version. |
| `Keep remote` | Resolve a conflict by downloading the remote version. |
| `Mark synced` | Mark matching local and remote files as synced. |
| `Delete` | Trash or delete a tracked file, with confirmation. |
| `Open` | Reveal the local file in Finder. |

## Sync Behavior

`ysync` compares three things:

- the last saved sync state
- the current local folder
- the current remote WebDAV tree

This avoids treating every local/remote difference as a conflict on the first run.

Default behavior:

| Situation | Default action |
| --- | --- |
| New local file | Upload |
| Remote-only untracked file | Ignore |
| Local changed, remote unchanged | Upload |
| Remote changed, local unchanged | Download |
| Both changed | Conflict |
| Local deleted, remote unchanged | Trash remote |
| Remote deleted, local unchanged | Trash local |

Deletes are enabled by default and use trash mode. To permanently delete instead:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --delete-mode hard
```

To disable delete propagation:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --delete=false
```

## Ignored Files

By default, `ysync` ignores common system and project files, including:

- `.DS_Store`
- `.git/`, `.svn/`, `.hg/`
- `node_modules/`
- `vendor/`
- hidden files beginning with `.`
- temporary sync files such as `*.ysync.*`
- conflict files such as `*.conflict-*`

Include hidden files:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --include-hidden
```

Use a custom ignore file:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --ignore-file "$HOME/.ysyncignore"
```

## Options

| Flag | Default | Description |
| --- | --- | --- |
| `-local` | required | Local folder to sync. |
| `-remote` | `/` | Remote Yandex Disk folder. |
| `-url` | `https://webdav.yandex.ru` | WebDAV base URL. |
| `-user` | empty | Yandex login. |
| `-pass` | empty | Yandex app password. |
| `-token` | empty | Yandex OAuth token used as the password value. |
| `-keychain-profile` | empty | Keychain service name to read credentials from. |
| `-conflict` | `skip` | Conflict strategy: `skip`, `local`, or `remote`. |
| `-delete` | `true` | Propagate deletes for tracked files. |
| `-delete-mode` | `trash` | Delete mode: `trash` or `hard`. |
| `-import-remote` | `false` | Include remote-only untracked files in the plan. |
| `-init-strategy` | `skip-conflicts` | First-run strategy. |
| `-interval` | `5m` | Remote scan interval and cache TTL. |
| `-debounce` | `3s` | Delay before syncing a stable local file change. |
| `-local-scan-interval` | `1m` | Polling interval if filesystem watching degrades. |
| `-remote-workers` | `1` | Remote crawl worker count. |
| `-concurrency` | `3` | Max concurrent sync operations. |
| `-dry-run` | `false` | Print the plan without changing files. |
| `-temp-ttl` | `24h` | Local temp file age before cleanup. |
| `-remote-temp-ttl` | `24h` | Remote upload temp age before cleanup. |
| `-show-cleanup` | `false` | Include cleanup actions in dry-run output. |
| `-bandwidth` | `0` | Max bytes per second per transfer, where `0` means unlimited. |
| `-ui-addr` | `127.0.0.1:8787` | Web UI listen address. |
| `-log-file` | empty | Structured log file path. |
| `-verbose` | `false` | Enable debug logging. |
| `-state` | `~/.ysync/state.json` | Sync state file path. |
| `-remote-cache` | `~/.ysync/remote-cache.json` | Remote metadata cache path. |
| `-print-launch-agent` | `false` | Print a LaunchAgent plist and exit. |
| `-version` | `false` | Print the version and exit. |

## Files Created

Default user files:

| Path | Purpose |
| --- | --- |
| `~/.local/bin/ysync` | Installed binary from `make install-macos`. |
| `~/Library/LaunchAgents/com.ysync.service.plist` | macOS LaunchAgent definition. |
| `~/Library/Logs/ysync/stdout.log` | LaunchAgent standard output. |
| `~/Library/Logs/ysync/stderr.log` | LaunchAgent standard error. |
| `~/.ysync/state.json` | Sync state. |
| `~/.ysync/remote-cache.json` | Remote metadata cache. |
| `~/.ysync/trash/` | Local trash used by safe delete mode. |

## Troubleshooting

Check the planned changes before changing files:

```bash
bin/ysync -local "$HOME/SyncFolder" -remote / --dry-run --verbose
```

If authentication fails, confirm Keychain entries:

```bash
security find-generic-password -s ysync -a yandex_user -w
security find-generic-password -s ysync -a yandex_pass -w
```

If the watcher reports too many open files, `ysync` falls back to polling. You can keep using the service, or increase macOS file limits if you need recursive event watching for a large tree.

If the service will not start, inspect logs:

```bash
tail -n 100 "$HOME/Library/Logs/ysync/stderr.log"
```

## Development

Useful commands:

```bash
make help
make build
make test
make dry-run LOCAL_FOLDER="$HOME/SyncFolder"
make run LOCAL_FOLDER="$HOME/SyncFolder"
```

The project is a Go module with the command entrypoint in `main.go` and implementation under `ysync/`.

## Release

GitHub Actions publishes releases for version tags:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow runs tests, builds Darwin and Linux archives for `amd64` and `arm64`, uploads checksums, and names macOS assets in the format expected by `scripts/install.sh`, such as `ysync_Darwin_arm64.tar.gz`.
