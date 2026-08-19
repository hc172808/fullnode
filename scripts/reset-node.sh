#!/usr/bin/env bash
# Reset a GYDS node to the first-run setup wizard.
#
# Deletes the server .env and configured runtime data. This includes chain
# state, node identity, admin/PIN state, setup markers, and local keystores.
# It never deletes source code or the Git repository.
set -euo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_DIR="${GYDS_APP_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
ENV_FILE="${GYDS_ENV_FILE:-$APP_DIR/.env}"
DATA_DIR="${GYDS_DATA_DIR:-}"
ASSUME_YES=false
DRY_RUN=false
SERVICE_NAME="${GYDS_SERVICE_NAME:-gyds-fullnode}"
COMPOSE_SERVICE_NAME="${GYDS_COMPOSE_SERVICE_NAME:-gyds-fullnode-compose}"
COMPOSE_FILE="$APP_DIR/docker-compose.yml"
native_was_active=false
compose_was_active=false

log() { printf '[GYDS reset] %s\n' "$*"; }
warn() { printf '[GYDS reset] WARNING: %s\n' "$*" >&2; }
die() { printf '[GYDS reset] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Reset a GYDS node and reopen the web setup wizard.

Options:
  --yes        Skip the confirmation prompt.
  --dry-run    Show what would be removed without changing anything.
  --help       Show this help.

Environment overrides:
  GYDS_APP_DIR     Application checkout/install directory.
  GYDS_ENV_FILE    Configuration file (default: <app-dir>/.env).
  GYDS_DATA_DIR    Runtime data directory (otherwise read from .env).
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes|-y) ASSUME_YES=true; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "Unknown option: $1 (use --help)" ;;
  esac
done

[[ -d "$APP_DIR" ]] || die "Application directory not found: $APP_DIR"

env_value() {
  local key="$1"
  [[ -f "$ENV_FILE" ]] || return 0
  sed -n "s/^[[:space:]]*${key}[[:space:]]*=[[:space:]]*//p" "$ENV_FILE" |
    tail -1 | sed -E "s/^'(.*)'$/\1/; s/^\"(.*)\"$/\1/" | tr -d '\r'
}

if [[ -z "$DATA_DIR" ]]; then
  DATA_DIR="$(env_value GYDS_DATA_DIR)"
fi
if [[ -z "$DATA_DIR" ]]; then
  [[ "$APP_DIR" == "/opt/gyds-fullnode" ]] &&
    DATA_DIR="/var/lib/gyds-fullnode" ||
    DATA_DIR="$APP_DIR/data"
fi
[[ "$DATA_DIR" == /* ]] || DATA_DIR="$APP_DIR/${DATA_DIR#./}"

APP_REAL="$(realpath -m -- "$APP_DIR" 2>/dev/null || printf '%s' "$APP_DIR")"
DATA_REAL="$(realpath -m -- "$DATA_DIR" 2>/dev/null || printf '%s' "$DATA_DIR")"
[[ "$DATA_REAL" != "/" && "$DATA_REAL" != "$APP_REAL" ]] ||
  die "Refusing unsafe data directory: $DATA_REAL"
[[ ! -L "$DATA_DIR" ]] || die "Refusing a symlinked data directory: $DATA_DIR"
if [[ "$DATA_REAL" == "$APP_REAL/"* ]]; then
  [[ "$DATA_REAL" == "$APP_REAL/data" || "$DATA_REAL" == "$APP_REAL/data/"* ]] ||
    die "Refusing to delete an application directory child other than data/: $DATA_REAL"
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null && native_was_active=true || true
  systemctl is-active --quiet "$COMPOSE_SERVICE_NAME" 2>/dev/null && compose_was_active=true || true
fi

log "Application directory: $APP_DIR"
log "Configuration file:    $ENV_FILE"
log "Data directory:        $DATA_DIR"
log "Native service active: $native_was_active"
log "Compose service active: $compose_was_active"
warn "This permanently deletes the node configuration and runtime data."

if ! $ASSUME_YES && ! $DRY_RUN; then
  [[ -t 0 ]] || die "Confirmation required in non-interactive mode; use --yes."
  read -r -p "Type RESET to continue: " confirmation
  [[ "$confirmation" == "RESET" ]] || { log "Reset cancelled."; exit 0; }
fi
if $DRY_RUN; then
  log "Dry run complete; nothing was changed."
  exit 0
fi

if [[ $EUID -eq 0 ]] && command -v systemctl >/dev/null 2>&1; then
  if $native_was_active; then systemctl stop "$SERVICE_NAME"; fi
  if $compose_was_active; then systemctl stop "$COMPOSE_SERVICE_NAME" 2>/dev/null || true; fi
fi

if $compose_was_active && command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]]; then
  docker compose -f "$COMPOSE_FILE" down --volumes --remove-orphans
fi

rm -f -- "$ENV_FILE"
rm -rf -- "$DATA_DIR"
log "Removed configuration and runtime data."

if $native_was_active && command -v systemctl >/dev/null 2>&1; then
  systemctl start "$SERVICE_NAME"
  log "Native service restarted; open /setup."
elif $compose_was_active && command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]]; then
  docker compose -f "$COMPOSE_FILE" up -d
  log "Compose service restarted; open /setup."
else
  log "No managed service restarted; start the node and open /setup."
fi