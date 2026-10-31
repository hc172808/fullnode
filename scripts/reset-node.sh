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
BACKUP_DIR="${GYDS_RESET_BACKUP_DIR:-/var/backups/gyds-fullnode}"
ASSUME_YES=false
DRY_RUN=false
SERVICE_NAME="${GYDS_SERVICE_NAME:-gyds-fullnode}"
COMPOSE_SERVICE_NAME="${GYDS_COMPOSE_SERVICE_NAME:-gyds-fullnode-compose}"
COMPOSE_FILE="$APP_DIR/docker-compose.yml"
native_was_active=false
compose_was_active=false
compose_runtime_found=false
compose_containers=()

log() { printf '[GYDS reset] %s\n' "$*"; }
warn() { printf '[GYDS reset] WARNING: %s\n' "$*" >&2; }
die() { printf '[GYDS reset] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Reset a GYDS node and reopen the web setup wizard.

Options:
  --yes        Skip the confirmation prompt.
  --dry-run    Show what would be removed without changing anything.
  --backup-dir DIR  Store the protected reset backup in DIR.
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
    --backup-dir) [[ $# -ge 2 ]] || die "--backup-dir requires a directory"; BACKUP_DIR="$2"; shift 2 ;;
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
BACKUP_REAL="$(realpath -m -- "$BACKUP_DIR" 2>/dev/null || printf '%s' "$BACKUP_DIR")"
[[ "$DATA_REAL" != "/" && "$DATA_REAL" != "$APP_REAL" ]] ||
  die "Refusing unsafe data directory: $DATA_REAL"
[[ "$BACKUP_REAL" != "$DATA_REAL" && "$BACKUP_REAL" != "$DATA_REAL/"* ]] ||
  die "Backup directory must not be inside the data directory: $BACKUP_REAL"
[[ ! -L "$DATA_DIR" ]] || die "Refusing a symlinked data directory: $DATA_DIR"
if [[ "$DATA_REAL" == "$APP_REAL/"* ]]; then
  [[ "$DATA_REAL" == "$APP_REAL/data" || "$DATA_REAL" == "$APP_REAL/data/"* ]] ||
    die "Refusing to delete an application directory child other than data/: $DATA_REAL"
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null && native_was_active=true || true
  systemctl is-active --quiet "$COMPOSE_SERVICE_NAME" 2>/dev/null && compose_was_active=true || true
fi
if command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]] &&
   docker info >/dev/null 2>&1; then
  mapfile -t compose_containers < <(docker compose -f "$COMPOSE_FILE" ps -aq 2>/dev/null || true)
  ((${#compose_containers[@]} > 0)) && compose_runtime_found=true
fi

log "Application directory: $APP_DIR"
log "Configuration file:    $ENV_FILE"
log "Data directory:        $DATA_DIR"
log "Backup directory:      $BACKUP_DIR"
log "Native service active: $native_was_active"
log "Compose service active: $compose_was_active"
log "Compose containers found: ${#compose_containers[@]}"
warn "This removes the node configuration and runtime data after creating a backup."

if ! $ASSUME_YES && ! $DRY_RUN; then
  [[ -t 0 ]] || die "Confirmation required in non-interactive mode; use --yes."
  read -r -p "Type RESET to continue: " confirmation
  [[ "$confirmation" == "RESET" ]] || { log "Reset cancelled."; exit 0; }
fi
if $DRY_RUN; then
  log "Dry run complete; nothing was changed."
  exit 0
fi

[[ $EUID -eq 0 ]] || die "Run the reset as root (for example: sudo scripts/reset-node.sh)."

# Capture Compose's persistent data volume before stopping/removing containers.
docker_volume_names=()
docker_volume_mountpoints=()
if { $compose_was_active || $compose_runtime_found; } &&
   command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]]; then
  for container_id in "${compose_containers[@]}"; do
    mount_info="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Type}}|{{.Name}}|{{.Source}}{{end}}{{end}}' "$container_id" 2>/dev/null || true)"
    IFS='|' read -r mount_type volume_name mount_source <<< "$mount_info"
    if [[ "$mount_type" == "volume" && -n "$volume_name" ]]; then
      duplicate=false
      for saved_volume in "${docker_volume_names[@]}"; do
        [[ "$saved_volume" == "$volume_name" ]] && duplicate=true
      done
      if ! $duplicate; then
        mountpoint="$(docker volume inspect --format '{{.Mountpoint}}' "$volume_name" 2>/dev/null || true)"
        [[ -n "$mountpoint" && -d "$mountpoint" ]] ||
          die "Could not locate Docker data volume $volume_name for backup."
        docker_volume_names+=("$volume_name")
        docker_volume_mountpoints+=("$mountpoint")
      fi
    elif [[ "$mount_type" == "bind" && -n "$mount_source" ]]; then
      mount_real="$(realpath -m -- "$mount_source")"
      [[ "$mount_real" == "$DATA_REAL" ]] ||
        die "Compose bind mount $mount_real does not match the configured data directory $DATA_REAL; refusing reset."
    fi
  done
fi

if [[ $EUID -eq 0 ]] && command -v systemctl >/dev/null 2>&1; then
  if $native_was_active; then systemctl stop "$SERVICE_NAME"; fi
  if $compose_was_active; then systemctl stop "$COMPOSE_SERVICE_NAME" 2>/dev/null || true; fi
fi

timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
backup_path="$(mktemp -d "${BACKUP_DIR}/reset-${timestamp}.XXXXXX")"
chmod 700 "$backup_path"
backup_created=false
if [[ -d "$DATA_DIR" ]]; then
  tar -czpf "${backup_path}/node-data.tar.gz" -C "$(dirname "$DATA_REAL")" "$(basename "$DATA_REAL")"
  backup_created=true
fi
if [[ -f "$ENV_FILE" ]]; then
  cp -p -- "$ENV_FILE" "${backup_path}/environment.env"
  chmod 600 "${backup_path}/environment.env"
  backup_created=true
fi
for i in "${!docker_volume_names[@]}"; do
  volume_name="${docker_volume_names[$i]}"
  volume_path="${docker_volume_mountpoints[$i]}"
  tar -czpf "${backup_path}/docker-volume-${volume_name}.tar.gz" -C "$volume_path" .
  backup_created=true
done
if $backup_created; then
  chmod 600 "$backup_path"/*
  log "Protected backup created at: $backup_path"
else
  rmdir "$backup_path"
  log "No configuration or runtime data was present to back up."
fi

if { $compose_was_active || $compose_runtime_found; } &&
   command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]]; then
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
elif $compose_runtime_found && command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE_FILE" ]]; then
  docker compose -f "$COMPOSE_FILE" up -d
  log "Compose node restarted; open /setup."
else
  log "No managed service restarted; start the node and open /setup."
fi