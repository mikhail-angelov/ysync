#!/bin/sh
set -eu

APP_NAME="ysync"
LABEL="com.ysync.service"
DEFAULT_REPO="mikhail-angelov/ysync"

SOURCE="github"
REPO="$DEFAULT_REPO"
VERSION="latest"
BINARY_PATH=""
INSTALL_DIR="${HOME}/.local/bin"
LOCAL_FOLDER=""
REMOTE_FOLDER="/"
URL="https://webdav.yandex.ru"
CONFLICT="skip"
DELETE_MODE="trash"
KEYCHAIN_PROFILE=""
UI_ADDR="127.0.0.1:8787"
UNINSTALL="0"
REMOVE_BINARY="0"

usage() {
  cat <<EOF
Usage:
  install.sh [options]

Install ysync as a per-user macOS LaunchAgent.

Options:
  --source github|local       Install from GitHub release or local checkout (default: github)
  --repo owner/name           GitHub repository for --source github (default: ${DEFAULT_REPO})
  --version vX.Y.Z|latest     GitHub release tag (default: latest)
  --binary path               Existing local binary to install
  --install-dir path          Binary install directory (default: ~/.local/bin)
  --local-folder path         Local folder to sync (required for install)
  --remote-folder path        Remote Yandex Disk folder (default: /)
  --url url                   WebDAV base URL (default: https://webdav.yandex.ru)
  --conflict skip|local|remote
                              Conflict strategy (default: skip)
  --delete-mode trash|hard    Delete mode (default: trash)
  --keychain-profile name     macOS Keychain profile for credentials
  --ui-addr address           Local web UI listen address (default: 127.0.0.1:8787)
  --uninstall                 Remove LaunchAgent
  --remove-binary             With --uninstall, also remove installed binary
  -h, --help                  Show this help

Environment:
  YANDEX_USER                 Yandex login to store in Keychain during install
  YANDEX_PASS                 Yandex app password to store in Keychain during install
  YANDEX_TOKEN                OAuth token to store as the WebDAV password value

Examples:
  ./scripts/install.sh --source local --local-folder ~/SyncFolder
  ./scripts/install.sh --source github --version latest --local-folder ~/SyncFolder
  YANDEX_USER=you YANDEX_PASS=app-password ./scripts/install.sh --local-folder ~/SyncFolder
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

expand_path() {
  case "$1" in
    "") printf '\n' ;;
    "~") printf '%s\n' "$HOME" ;;
    "~/"*) printf '%s/%s\n' "$HOME" "${1#~/}" ;;
    *) printf '%s\n' "$1" ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --source)
      [ "$#" -ge 2 ] || die "--source requires a value"
      SOURCE="$2"
      shift 2
      ;;
    --repo)
      [ "$#" -ge 2 ] || die "--repo requires a value"
      REPO="$2"
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || die "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --binary)
      [ "$#" -ge 2 ] || die "--binary requires a value"
      BINARY_PATH="$2"
      shift 2
      ;;
    --install-dir)
      [ "$#" -ge 2 ] || die "--install-dir requires a value"
      INSTALL_DIR="$2"
      shift 2
      ;;
    --local-folder)
      [ "$#" -ge 2 ] || die "--local-folder requires a value"
      LOCAL_FOLDER="$2"
      shift 2
      ;;
    --remote-folder)
      [ "$#" -ge 2 ] || die "--remote-folder requires a value"
      REMOTE_FOLDER="$2"
      shift 2
      ;;
    --url)
      [ "$#" -ge 2 ] || die "--url requires a value"
      URL="$2"
      shift 2
      ;;
    --conflict)
      [ "$#" -ge 2 ] || die "--conflict requires a value"
      CONFLICT="$2"
      shift 2
      ;;
    --delete-mode)
      [ "$#" -ge 2 ] || die "--delete-mode requires a value"
      DELETE_MODE="$2"
      shift 2
      ;;
    --keychain-profile)
      [ "$#" -ge 2 ] || die "--keychain-profile requires a value"
      KEYCHAIN_PROFILE="$2"
      shift 2
      ;;
    --ui-addr)
      [ "$#" -ge 2 ] || die "--ui-addr requires a value"
      UI_ADDR="$2"
      shift 2
      ;;
    --uninstall)
      UNINSTALL="1"
      shift
      ;;
    --remove-binary)
      REMOVE_BINARY="1"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[ "$(uname -s)" = "Darwin" ] || die "macOS is required"

case "$SOURCE" in
  github|local) ;;
  *) die "--source must be github or local" ;;
esac
case "$CONFLICT" in
  skip|local|remote) ;;
  *) die "--conflict must be skip, local, or remote" ;;
esac
case "$DELETE_MODE" in
  trash|hard) ;;
  *) die "--delete-mode must be trash or hard" ;;
esac

INSTALL_DIR="$(expand_path "$INSTALL_DIR")"
BINARY_PATH="$(expand_path "$BINARY_PATH")"
LOCAL_FOLDER="$(expand_path "$LOCAL_FOLDER")"
BIN_PATH="${INSTALL_DIR}/${APP_NAME}"
PLIST_DIR="${HOME}/Library/LaunchAgents"
PLIST_PATH="${PLIST_DIR}/${LABEL}.plist"

unload_agent() {
  if [ -f "$PLIST_PATH" ]; then
    launchctl bootout "gui/$(id -u)" "$PLIST_PATH" >/dev/null 2>&1 || true
  else
    launchctl bootout "gui/$(id -u)/${LABEL}" >/dev/null 2>&1 || true
  fi
}

if [ "$UNINSTALL" = "1" ]; then
  unload_agent
  rm -f "$PLIST_PATH"
  if [ "$REMOVE_BINARY" = "1" ]; then
    rm -f "$BIN_PATH"
  fi
  echo "Uninstalled ${LABEL}"
  exit 0
fi

[ -n "$LOCAL_FOLDER" ] || die "--local-folder is required"
[ -d "$LOCAL_FOLDER" ] || die "local folder does not exist: $LOCAL_FOLDER"

mkdir -p "$INSTALL_DIR" "$PLIST_DIR"

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

install_from_local() {
  if [ -n "$BINARY_PATH" ]; then
    [ -f "$BINARY_PATH" ] || die "binary does not exist: $BINARY_PATH"
    install -m 0755 "$BINARY_PATH" "$BIN_PATH"
    return
  fi

  [ -f "go.mod" ] || die "--source local must run from the repository root, or pass --binary"
  need_cmd go
  go build -trimpath -o "$TMP_DIR/${APP_NAME}" .
  install -m 0755 "$TMP_DIR/${APP_NAME}" "$BIN_PATH"
}

install_from_github() {
  need_cmd curl
  need_cmd tar

  machine="$(uname -m)"
  case "$machine" in
    arm64) arch="arm64" ;;
    x86_64) arch="amd64" ;;
    *) die "unsupported macOS architecture: $machine" ;;
  esac

  asset="${APP_NAME}_Darwin_${arch}.tar.gz"
  if [ "$VERSION" = "latest" ]; then
    url="https://github.com/${REPO}/releases/latest/download/${asset}"
  else
    url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  fi

  archive="$TMP_DIR/$asset"
  echo "Downloading $url"
  curl -fL "$url" -o "$archive"
  tar -xzf "$archive" -C "$TMP_DIR"

  extracted="$TMP_DIR/${APP_NAME}_Darwin_${arch}/${APP_NAME}"
  [ -f "$extracted" ] || die "release archive does not contain ${APP_NAME}"
  install -m 0755 "$extracted" "$BIN_PATH"
}

install_keychain_credentials() {
  if [ -z "${YANDEX_USER:-}" ] && [ -z "${YANDEX_PASS:-}" ] && [ -z "${YANDEX_TOKEN:-}" ]; then
    return
  fi

  need_cmd security
  profile="$KEYCHAIN_PROFILE"
  if [ -z "$profile" ]; then
    profile="ysync"
    KEYCHAIN_PROFILE="$profile"
  fi

  if [ -n "${YANDEX_USER:-}" ]; then
    security add-generic-password -U -s "$profile" -a yandex_user -w "$YANDEX_USER" >/dev/null
  fi
  if [ -n "${YANDEX_PASS:-}" ]; then
    security add-generic-password -U -s "$profile" -a yandex_pass -w "$YANDEX_PASS" >/dev/null
  elif [ -n "${YANDEX_TOKEN:-}" ]; then
    security add-generic-password -U -s "$profile" -a yandex_pass -w "$YANDEX_TOKEN" >/dev/null
  fi
}

case "$SOURCE" in
  local) install_from_local ;;
  github) install_from_github ;;
esac

install_keychain_credentials
unload_agent

if [ -n "$KEYCHAIN_PROFILE" ]; then
  "$BIN_PATH" -local "$LOCAL_FOLDER" -remote "$REMOTE_FOLDER" -url "$URL" -conflict "$CONFLICT" -delete-mode "$DELETE_MODE" -ui-addr "$UI_ADDR" -keychain-profile "$KEYCHAIN_PROFILE" --print-launch-agent > "$PLIST_PATH"
else
  "$BIN_PATH" -local "$LOCAL_FOLDER" -remote "$REMOTE_FOLDER" -url "$URL" -conflict "$CONFLICT" -delete-mode "$DELETE_MODE" -ui-addr "$UI_ADDR" --print-launch-agent > "$PLIST_PATH"
fi

chmod 0644 "$PLIST_PATH"
launchctl bootstrap "gui/$(id -u)" "$PLIST_PATH"
launchctl enable "gui/$(id -u)/${LABEL}" >/dev/null 2>&1 || true
launchctl kickstart -k "gui/$(id -u)/${LABEL}"

echo "Installed ${APP_NAME}"
echo "Binary: ${BIN_PATH}"
echo "LaunchAgent: ${PLIST_PATH}"
echo "Local folder: ${LOCAL_FOLDER}"
echo "Remote folder: ${REMOTE_FOLDER}"
echo "Web UI: http://${UI_ADDR}"
if [ -n "$KEYCHAIN_PROFILE" ]; then
  echo "Keychain profile: ${KEYCHAIN_PROFILE}"
fi
