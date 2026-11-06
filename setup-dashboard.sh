#!/usr/bin/env bash
# Interactive dashboard settings editor.
# This edits dashboard-related variables in an existing node environment file;
# it does not install, restart, or modify node data.
set -euo pipefail

usage() {
  cat <<'EOF'
GYDS dashboard setup

Usage:
  bash setup-dashboard.sh [--env FILE]

Updates only:
  GYDS_DASHBOARD_ENABLED
  GYDS_DASHBOARD_PORT
  GYDS_EXTERNAL_URL
  (enter - for the external URL to clear it)

The dashboard runs inside the node process; this script does not create a
separate dashboard service. Admin login wallet/PIN settings are not written
here. The authorized Admin wallet is fixed by the current application, and the
node PIN is managed through first-run setup or authenticated PIN recovery.
EOF
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

ENV_FILE="${GYDS_ENV_FILE:-.env}"
while (($#)); do
  case "$1" in
    --env)
      (($# >= 2)) || die "--env requires a file path"
      ENV_FILE="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1 (use --help)"
      ;;
  esac
done

[[ -t 0 ]] || die "interactive input is required; run this script in a terminal"
[[ -f "$ENV_FILE" ]] || die "environment file not found: $ENV_FILE; run setup-node.sh first"
[[ ! -L "$ENV_FILE" ]] || die "refusing to replace a symbolic link: $ENV_FILE"
[[ -w "$ENV_FILE" ]] || die "environment file is not writable: $ENV_FILE"

read_env_value() {
  local key="$1"
  local line value
  line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?${key}=" -- "$ENV_FILE" | tail -n 1 || true)"
  value="${line#*=}"
  value="${value%$'\r'}"
  value="${value#\"}"
  value="${value%\"}"
  value="${value#\'}"
  value="${value%\'}"
  printf '%s' "$value"
}

prompt_value() {
  local name="$1" label="$2" default="$3" answer
  read -r -p "$label [$default]: " answer
  printf -v "$name" '%s' "${answer:-$default}"
}

prompt_yes_no() {
  local name="$1" label="$2" default="$3" answer
  if [[ "$default" == "true" ]]; then
    read -r -p "$label [Y/n]: " answer
    answer="${answer:-y}"
  else
    read -r -p "$label [y/N]: " answer
    answer="${answer:-n}"
  fi
  case "${answer,,}" in
    y|yes|true|1) printf -v "$name" '%s' "true" ;;
    n|no|false|0) printf -v "$name" '%s' "false" ;;
    *) die "answer yes or no to: $label" ;;
  esac
}

validate_port() {
  local name="$1" value="$2" number
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a number"
  number=$((10#$value))
  ((number >= 1 && number <= 65535)) || die "$name must be between 1 and 65535"
}

enabled_default="$(read_env_value GYDS_DASHBOARD_ENABLED)"
case "${enabled_default,,}" in
  false|no|0|off) enabled_default=false ;;
  *) enabled_default=true ;;
esac
port_default="$(read_env_value GYDS_DASHBOARD_PORT)"
port_default="${port_default:-5000}"
external_default="$(read_env_value GYDS_EXTERNAL_URL)"
node_mode="$(read_env_value GYDS_NODE_MODE)"

prompt_yes_no DASHBOARD_ENABLED "Enable the web dashboard" "$enabled_default"
prompt_value DASHBOARD_PORT "Dashboard port" "$port_default"
validate_port "Dashboard port" "$DASHBOARD_PORT"
if [[ "$node_mode" == "testnode" && "$DASHBOARD_PORT" != "15000" ]]; then
  die "testnode dashboard port is fixed at 15000 by the installer"
fi
prompt_value EXTERNAL_URL "Public HTTPS URL (blank if not configured)" "$external_default"
[[ "$EXTERNAL_URL" != "-" ]] || EXTERNAL_URL=""
if [[ -n "$EXTERNAL_URL" && "$EXTERNAL_URL" != https://* ]]; then
  die "GYDS_EXTERNAL_URL must begin with https:// or be left blank"
fi

for other_key in GYDS_RPC_PORT GYDS_WS_PORT GYDS_P2P_PORT; do
  other_port="$(read_env_value "$other_key")"
  if [[ -n "$other_port" && "$other_port" == "$DASHBOARD_PORT" ]]; then
    die "dashboard port conflicts with $other_key ($other_port)"
  fi
done

printf '\nSettings to save to %s:\n' "$ENV_FILE"
printf '  Dashboard enabled: %s\n' "$DASHBOARD_ENABLED"
printf '  Dashboard port:   %s\n' "$DASHBOARD_PORT"
printf '  External URL:     %s\n' "${EXTERNAL_URL:-<not configured>}"
read -r -p "Apply these settings? [y/N]: " confirm
case "${confirm,,}" in
  y|yes) ;;
  *) printf 'No changes made.\n'; exit 0 ;;
esac

tmp_file="$(mktemp -- "${ENV_FILE}.tmp.XXXXXX")"
backup_file="${ENV_FILE}.bak.$(date -u +%Y%m%dT%H%M%SZ).$$"
cleanup() {
  [[ -z "${tmp_file:-}" || ! -e "$tmp_file" ]] || rm -f -- "$tmp_file"
}
trap cleanup EXIT

cp -p -- "$ENV_FILE" "$backup_file"
chmod 600 "$backup_file"
awk '
  /^[[:space:]]*(export[[:space:]]+)?GYDS_DASHBOARD_ENABLED[[:space:]]*=/ { next }
  /^[[:space:]]*(export[[:space:]]+)?GYDS_DASHBOARD_PORT[[:space:]]*=/ { next }
  /^[[:space:]]*(export[[:space:]]+)?GYDS_EXTERNAL_URL[[:space:]]*=/ { next }
  { print }
' "$ENV_FILE" > "$tmp_file"
{
  printf '\n# Dashboard settings written by setup-dashboard.sh\n'
  printf 'GYDS_DASHBOARD_ENABLED=%q\n' "$DASHBOARD_ENABLED"
  printf 'GYDS_DASHBOARD_PORT=%q\n' "$DASHBOARD_PORT"
  if [[ -n "$EXTERNAL_URL" ]]; then
    printf 'GYDS_EXTERNAL_URL=%q\n' "$EXTERNAL_URL"
  fi
} >> "$tmp_file"
chmod 600 "$tmp_file"
mv -- "$tmp_file" "$ENV_FILE"
tmp_file=""

printf '\nDashboard settings saved. Backup: %s\n' "$backup_file"
printf 'No service was restarted and no chain data was changed.\n'
printf 'Restart only the node runtime you already use to apply these settings.\n'
