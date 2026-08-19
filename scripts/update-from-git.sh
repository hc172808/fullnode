#!/usr/bin/env bash
# Pull, build, and safely restart an installed GYDS node.
#
# This script is installed as /usr/local/bin/gyds-fullnode-update by the
# production installer. It deliberately does not run `git pull` blindly:
# local code changes and non-fast-forward updates are refused, while ignored
# runtime files such as .env and chain data are preserved.
set -euo pipefail
IFS=$'\n\t'

APP_DIR="${GYDS_APP_DIR:-/opt/gyds-fullnode}"
DATA_DIR="${GYDS_DATA_DIR:-/var/lib/gyds-fullnode}"
BRANCH="${GYDS_UPDATE_BRANCH:-main}"
SERVICE_NAME="gyds-fullnode"
COMPOSE_SERVICE_NAME="gyds-fullnode-compose"
BACKUP_ROOT="${GYDS_BACKUP_DIR:-/var/backups/gyds-fullnode}"
LOCK_FILE="/run/lock/gyds-fullnode-update.lock"
HEALTH_TIMEOUT="${GYDS_UPDATE_HEALTH_TIMEOUT:-90}"

log()  { printf '[GYDS update] %s\n' "$*"; }
warn() { printf '[GYDS update] WARNING: %s\n' "$*" >&2; }
die()  { printf '[GYDS update] ERROR: %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "Run as root: sudo gyds-fullnode-update"
[[ -d "$APP_DIR/.git" ]] || die "Git checkout not found: $APP_DIR"
command -v git >/dev/null || die "git is required"

# Read only the data/port settings needed by this script. Do not source .env:
# older installations may contain unquoted values, and .env may contain
# sensitive wallet material.
env_value() {
  local key="$1"
  sed -n "s/^[[:space:]]*${key}[[:space:]]*=[[:space:]]*//p" "$APP_DIR/.env" 2>/dev/null |
    tail -1 | sed -E "s/^'(.*)'$/\1/; s/^\"(.*)\"$/\1/" | tr -d '\r'
}

DATA_DIR="${GYDS_DATA_DIR:-$(env_value GYDS_DATA_DIR)}"
DATA_DIR="${DATA_DIR:-/var/lib/gyds-fullnode}"

mkdir -p "$(dirname "$LOCK_FILE")" "$BACKUP_ROOT"
exec 9>"$LOCK_FILE"
flock -n 9 || die "Another update is already running"

cd "$APP_DIR"
git config --global --add safe.directory "$APP_DIR" 2>/dev/null || true

if [[ -n "$(git status --porcelain --untracked-files=all)" ]]; then
  die "Working tree is not clean. Commit or remove local code changes before updating."
fi

OLD_SHA="$(git rev-parse HEAD)"
REMOTE="origin/$BRANCH"
STAMP="$(date -u +%Y%m%d_%H%M%S)"
BACKUP_DIR="$BACKUP_ROOT/$STAMP"
mkdir -p "$BACKUP_DIR"

SERVICE_MODE="native"
if systemctl is-enabled --quiet "$COMPOSE_SERVICE_NAME" 2>/dev/null ||
   systemctl is-active --quiet "$COMPOSE_SERVICE_NAME" 2>/dev/null; then
  SERVICE_MODE="docker"
elif systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null ||
     systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  SERVICE_MODE="native"
elif command -v docker >/dev/null &&
     [[ -f "$APP_DIR/docker-compose.yml" ]] &&
     docker compose -f "$APP_DIR/docker-compose.yml" ps -q 2>/dev/null | grep -q .; then
  SERVICE_MODE="docker"
else
  die "No managed GYDS service found (expected $SERVICE_NAME or $COMPOSE_SERVICE_NAME)."
fi

if [[ "$SERVICE_MODE" == "docker" ]] && ! command -v docker >/dev/null; then
  die "Docker is required for the installed Docker service."
fi

RPC_PORT="${GYDS_RPC_PORT:-$(env_value GYDS_RPC_PORT | tr -d '[:space:]')}"
DASHBOARD_PORT="${GYDS_DASHBOARD_PORT:-$(env_value GYDS_DASHBOARD_PORT | tr -d '[:space:]')}"
RPC_PORT="${RPC_PORT:-8545}"
DASHBOARD_PORT="${DASHBOARD_PORT:-5000}"

log "Fetching origin/$BRANCH..."
git fetch --prune origin "$BRANCH"
git show-ref --verify --quiet "refs/remotes/$REMOTE" || die "Remote branch not found: $REMOTE"

NEW_SHA="$(git rev-parse "$REMOTE")"
if [[ "$OLD_SHA" == "$NEW_SHA" ]]; then
  log "Already up to date at ${OLD_SHA:0:12}."
  exit 0
fi

git merge-base --is-ancestor "$OLD_SHA" "$NEW_SHA" ||
  die "Update is not fast-forward. Review the branch history and resolve it manually."

log "Updating ${OLD_SHA:0:12} -> ${NEW_SHA:0:12}..."
git merge --ff-only "$REMOTE"

backup_path() {
  local path="$1"
  [[ -e "$path" ]] || return 0
  mkdir -p "$BACKUP_DIR/$(dirname "${path#"$DATA_DIR"/}")"
  cp -a "$path" "$BACKUP_DIR/${path#"$DATA_DIR"/}"
}

build_failed() {
  warn "Build failed; restoring the previous source revision."
  git reset --hard "$OLD_SHA" >/dev/null || true
  die "Update was not applied. The running service was not stopped."
}

if [[ "$SERVICE_MODE" == "docker" ]]; then
  command -v docker >/dev/null || build_failed
  docker compose -f "$APP_DIR/docker-compose.yml" build || build_failed
else
  command -v go >/dev/null || build_failed
  GOTOOLCHAIN=local go test ./... || build_failed
  GOTOOLCHAIN=local go build -ldflags="-s -w" -o /tmp/gyds-fullnode-new . || build_failed
fi

# Stop before copying LevelDB files so the backup is consistent.
if [[ "$SERVICE_MODE" == "docker" ]]; then
  systemctl stop "$COMPOSE_SERVICE_NAME" 2>/dev/null ||
    docker compose -f "$APP_DIR/docker-compose.yml" down
else
  systemctl stop "$SERVICE_NAME"
fi

backup_path "$DATA_DIR/state.db"
backup_path "$DATA_DIR/node.key"
backup_path "$DATA_DIR/admin"
backup_path "$DATA_DIR/keystore"
[[ -f "$APP_DIR/.env" ]] && cp -a "$APP_DIR/.env" "$BACKUP_DIR/.env"
printf '%s\n' "$OLD_SHA" > "$BACKUP_DIR/previous-commit"
log "Backup created at $BACKUP_DIR"

restore_source() {
  git reset --hard "$OLD_SHA" >/dev/null
}

rollback_native() {
  warn "Rolling back source and native binary..."
  restore_source
  if [[ -f "$BACKUP_DIR/gyds-fullnode.previous" ]]; then
    install -m 0755 "$BACKUP_DIR/gyds-fullnode.previous" /usr/local/bin/gyds-fullnode
  else
    GOTOOLCHAIN=local go build -ldflags="-s -w" -o /tmp/gyds-fullnode-rollback .
    install -m 0755 /tmp/gyds-fullnode-rollback /usr/local/bin/gyds-fullnode
    rm -f /tmp/gyds-fullnode-rollback
  fi
  systemctl start "$SERVICE_NAME"
}

rollback_docker() {
  warn "Rolling back source and Docker image..."
  restore_source
  docker compose -f "$APP_DIR/docker-compose.yml" build
  systemctl start "$COMPOSE_SERVICE_NAME" 2>/dev/null ||
    docker compose -f "$APP_DIR/docker-compose.yml" up -d
}

if [[ "$SERVICE_MODE" == "docker" ]]; then
  :
else
  install -m 0755 /usr/local/bin/gyds-fullnode "$BACKUP_DIR/gyds-fullnode.previous"
  install -m 0755 /tmp/gyds-fullnode-new /usr/local/bin/gyds-fullnode
  rm -f /tmp/gyds-fullnode-new
fi

if [[ "$SERVICE_MODE" == "docker" ]]; then
  systemctl start "$COMPOSE_SERVICE_NAME" 2>/dev/null ||
    docker compose -f "$APP_DIR/docker-compose.yml" up -d
else
  systemctl start "$SERVICE_NAME"
fi

healthy=false
deadline=$((SECONDS + HEALTH_TIMEOUT))
while (( SECONDS < deadline )); do
  if curl -fsS --max-time 3 "http://127.0.0.1:${RPC_PORT}/health" >/dev/null 2>&1 &&
     curl -fsS --max-time 3 "http://127.0.0.1:${DASHBOARD_PORT}/health" >/dev/null 2>&1; then
    healthy=true
    break
  fi
  sleep 2
done

if ! $healthy; then
  warn "Health checks failed after update."
  if [[ "$SERVICE_MODE" == "docker" ]]; then
    rollback_docker
  else
    rollback_native
  fi
  die "Update rolled back. Check journalctl -u $SERVICE_NAME and the backup at $BACKUP_DIR."
fi

log "Update complete at ${NEW_SHA:0:12}; $SERVICE_MODE service is healthy."
log "Backup retained at $BACKUP_DIR"