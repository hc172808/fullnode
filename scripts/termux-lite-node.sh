#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${HOME}/.local/share/gyds-lite"
CONFIG_DIR="${HOME}/.config/gyds-lite"
ENV_FILE="${CONFIG_DIR}/node.env"
PID_FILE="${APP_DIR}/node.pid"
LOG_FILE="${APP_DIR}/node.log"
PREFIX="${PREFIX:-${HOME}/.termux/usr}"
BIN="${PREFIX}/bin/gyds-fullnode"
SCRIPT_PATH="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
SOURCE_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'EOF'
GYDS Android lite-node helper for Termux

Install from a source checkout:
  bash scripts/termux-lite-node.sh install --bootstrap host:30303 \
    --allowed-node-ids <trusted-peer-node-id>[,...] [--network mainnet|testnet|testnet3b]

Commands after installation:
  gyds-lite start
  gyds-lite status
  gyds-lite logs
  gyds-lite stop

Options for install:
  --network NAME              Network profile (default: mainnet)
  --bootstrap ADDRESSES       Comma-separated trusted peer addresses (required)
  --allowed-node-ids IDS      Comma-separated trusted peer IDs (required)
  --help

The peer operator must also add this phone's node ID to their allowlist.
EOF
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

require_termux() {
  [[ -n "${TERMUX_VERSION:-}" || "${PREFIX}" == */com.termux/files/usr ]] ||
    die "run this script inside the Termux app on Android"
  command -v pkg >/dev/null 2>&1 || die "Termux package manager 'pkg' was not found"
  [[ "$(uname -m)" == "aarch64" ]] ||
    die "this installer currently supports Android arm64 only; detected $(uname -m)"
}

is_running() {
  [[ -s "$PID_FILE" ]] || return 1
  local pid
  pid="$(cat "$PID_FILE")"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  [[ -r "/proc/${pid}/cmdline" ]] || return 1
  tr '\0' ' ' < "/proc/${pid}/cmdline" | grep -Fq "$BIN"
}

configured_rpc_port() {
  local port
  port="$(sed -n 's/^GYDS_RPC_PORT=//p' "$ENV_FILE" 2>/dev/null | head -n 1)"
  printf '%s' "${port:-18545}"
}

validate_bootstrap_list() {
  local value="$1" entry host port
  local -a entries
  [[ -n "$value" ]] || return 1
  IFS=, read -r -a entries <<< "$value"
  for entry in "${entries[@]}"; do
    [[ -n "$entry" ]] || return 1
    case "$entry" in tcp://*) entry="${entry#tcp://}" ;; esac
    [[ "$entry" == *:* ]] || return 1
    host="${entry%:*}"
    port="${entry##*:}"
    [[ -n "$host" && "$port" =~ ^[0-9]{1,5}$ ]] || return 1
    ((10#$port > 0 && 10#$port <= 65535)) || return 1
    if [[ "$host" == \[*\] ]]; then
      host="${host:1:${#host}-2}"
      [[ "$host" =~ ^[A-Fa-f0-9:.]+$ ]] || return 1
    else
      [[ "$host" =~ ^[A-Za-z0-9._-]+$ ]] || return 1
    fi
  done
}

install_node() {
  require_termux
  local network="mainnet"
  local bootstrap=""
  local allowed_ids=""

  shift
  while (($#)); do
    case "$1" in
      --network)
        (($# >= 2)) || die "--network requires a value"
        network="$2"
        shift 2
        ;;
      --bootstrap)
        (($# >= 2)) || die "--bootstrap requires a value"
        bootstrap="$2"
        shift 2
        ;;
      --allowed-node-ids)
        (($# >= 2)) || die "--allowed-node-ids requires a value"
        allowed_ids="$2"
        shift 2
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        die "unknown install option: $1"
        ;;
    esac
  done

  case "$network" in
    mainnet|testnet|testnet3b) ;;
    *) die "unsupported network '$network' (use mainnet, testnet, or testnet3b)" ;;
  esac
  [[ -n "$bootstrap" ]] || die "provide at least one --bootstrap address"
  [[ -n "$allowed_ids" ]] || die "provide the bootstrap peer IDs with --allowed-node-ids"
  validate_bootstrap_list "$bootstrap" ||
    die "invalid bootstrap addresses; use comma-separated host:port values"
  [[ "$allowed_ids" =~ ^(0[xX])?[A-Fa-f0-9]{64}(,(0[xX])?[A-Fa-f0-9]{64})*$ ]] ||
    die "peer IDs must be 64-digit hexadecimal IDs, comma-separated"
  [[ -f "${SOURCE_DIR}/go.mod" ]] ||
    die "run this from the GYDS repository checkout; expected go.mod in ${SOURCE_DIR}"

  pkg update -y
  pkg install -y git golang curl
  mkdir -p "$APP_DIR" "$CONFIG_DIR" "${PREFIX}/bin"

  if [[ ! -x "$BIN" ]]; then
    printf 'Building the GYDS node for Termux/Android arm64...\n'
  fi
  (
    cd "$SOURCE_DIR"
    GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$BIN" .
  ) || die "Go build failed; check that the installed Go toolchain supports the project's go.mod version"
  chmod 0755 "$BIN"

  if [[ ! -e "$ENV_FILE" ]]; then
    umask 077
    cat > "$ENV_FILE" <<EOF
GYDS_NETWORK=${network}
GYDS_NODE_MODE=lite
GYDS_DATA_DIR=${APP_DIR}/data
GYDS_P2P_PORT=30303
GYDS_RPC_PORT=18545
GYDS_RPC_HOST=127.0.0.1
GYDS_DASHBOARD_PORT=15000
GYDS_BOOTSTRAP_NODES=${bootstrap}
GYDS_PEER_AUTH=true
GYDS_ALLOWED_NODES=${allowed_ids}
GYDS_LOG_LEVEL=info
EOF
    chmod 0600 "$ENV_FILE"
  else
    printf 'Keeping existing configuration; edit %s to change peers or network.\n' "$ENV_FILE"
  fi

  umask 077
  cat > "${PREFIX}/bin/gyds-lite" <<EOF
#!/usr/bin/env bash
exec "${SCRIPT_PATH}" "\$@"
EOF
  chmod 0755 "${PREFIX}/bin/gyds-lite"

  cat <<EOF

Installation complete.
  Start:  gyds-lite start
  Status: gyds-lite status
  Logs:   gyds-lite logs
  Stop:   gyds-lite stop

This is a trusted-peer header client, not a trustless wallet or state-proof
verifier. After starting, run 'gyds-lite status' and give the displayed node ID
to the peer operator so they can add this phone to their GYDS_ALLOWED_NODES.
Keep Android battery optimization disabled for Termux if you need continuous sync.
EOF
}

start_node() {
  [[ -x "$BIN" ]] || die "not installed; run 'gyds-lite install' from the repository checkout"
  [[ -f "$ENV_FILE" ]] || die "configuration missing: $ENV_FILE"
  if is_running; then
    printf 'GYDS lite node is already running (PID %s).\n' "$(cat "$PID_FILE")"
    return 0
  fi
  rm -f "$PID_FILE"
  mkdir -p "$APP_DIR"
  if command -v termux-wake-lock >/dev/null 2>&1; then
    termux-wake-lock || true
  else
    printf 'For continuous sync, install Termux:API and enable a wake lock.\n'
  fi
  (
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
    exec "$BIN" start
  ) >> "$LOG_FILE" 2>&1 </dev/null &
  echo "$!" > "$PID_FILE"
  chmod 0600 "$PID_FILE"
  printf 'Started GYDS lite node (PID %s); waiting for its local RPC endpoint...\n' "$(cat "$PID_FILE")"
  for ((attempt = 0; attempt < 20; attempt++)); do
    if ! is_running; then
      tail -n 40 "$LOG_FILE" 2>/dev/null || true
      die "node exited during startup"
    fi
    if curl -fsS --max-time 2 "http://127.0.0.1:$(configured_rpc_port)/p2p/node-id" >/dev/null 2>&1; then
      status_node
      return 0
    fi
    sleep 1
  done
  printf 'Node is running, but the RPC endpoint is not ready yet. Check: gyds-lite logs\n'
}

stop_node() {
  if ! is_running; then
    rm -f "$PID_FILE"
    printf 'GYDS lite node is not running.\n'
    return 0
  fi
  local pid
  pid="$(cat "$PID_FILE")"
  kill -TERM "$pid"
  for ((attempt = 0; attempt < 30; attempt++)); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$PID_FILE"
      command -v termux-wake-unlock >/dev/null 2>&1 && termux-wake-unlock || true
      printf 'GYDS lite node stopped cleanly.\n'
      return 0
    fi
    sleep 1
  done
  die "node has not stopped yet; it was not force-killed. Check the process and logs."
}

status_node() {
  if ! is_running; then
    printf 'GYDS lite node is not running.\n'
    return 1
  fi
  printf 'GYDS lite node is running (PID %s).\n' "$(cat "$PID_FILE")"
  curl -fsS --max-time 3 "http://127.0.0.1:$(configured_rpc_port)/p2p/node-id" ||
    printf 'RPC endpoint is not responding yet.\n'
  printf '\n'
  printf 'Sync progress:\n'
  curl -sS --max-time 3 "http://127.0.0.1:$(configured_rpc_port)/sync-status" ||
    printf 'Sync status endpoint is not responding yet.\n'
  printf '\nReadiness (HTTP 200 means ready; 503 means still syncing):\n'
  curl -sS --max-time 3 "http://127.0.0.1:$(configured_rpc_port)/ready" ||
    printf 'Readiness endpoint is not responding yet.\n'
  printf '\n'
}

command="${1:-}"
case "$command" in
  install)
    install_node "$@"
    ;;
  start)
    start_node
    ;;
  stop)
    stop_node
    ;;
  status)
    status_node
    ;;
  logs)
    [[ -f "$LOG_FILE" ]] || die "no log file yet; start the node first"
    tail -n 100 -f "$LOG_FILE"
    ;;
  --help|-h|help|"")
    usage
    ;;
  *)
    die "unknown command '$command' (use install, start, status, logs, or stop)"
    ;;
esac
