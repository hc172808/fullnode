#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
FLEET_ROOT="${GYDS_FLEET_HOME:-${XDG_STATE_HOME:-${HOME}/.local/state}/gyds-node-fleet}"
NODES_DIR="${FLEET_ROOT}/nodes"
IMAGE="gyds-fullnode-fleet:local"

usage() {
  cat <<'EOF'
Manage isolated GYDS Docker nodes on one server.

  docker-node-fleet.sh add NAME MODE NETWORK [BOOTSTRAP_ADDRESSES] [ALLOWED_PEER_IDS] [ADVERTISE_HOST]
  docker-node-fleet.sh list
  docker-node-fleet.sh start NAME
  docker-node-fleet.sh stop NAME
  docker-node-fleet.sh status NAME
  docker-node-fleet.sh logs NAME
  docker-node-fleet.sh delete NAME --delete-data --yes

Modes: full, lite, sync, boost
Networks: mainnet, testnet, testnet3b

The add command builds the current source, allocates distinct host ports, creates
an isolated named data volume, and starts the node. RPC and dashboard ports bind
to 127.0.0.1; only the P2P TCP port binds publicly. Open P2P ports in your
firewall and set ADVERTISE_HOST to the server's public DNS name or IP.

P2P peers require mutual allowlisting. Provide the remote peer IDs in
ALLOWED_PEER_IDS, and add this node's ID to each remote peer's allowlist.
Lite mode is trusted-peer header sync only, not a trustless wallet client.
Deleting a node's volume requires both --delete-data and --yes.
EOF
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

require_docker() {
  command -v docker >/dev/null 2>&1 || die "Docker is required"
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"
  docker info >/dev/null 2>&1 || die "Docker is unavailable or this account cannot access the Docker daemon"
}

valid_name() {
  [[ "$1" =~ ^[a-z][a-z0-9_-]{0,30}$ ]]
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

validate_node_ids() {
  local value="$1"
  [[ "$value" =~ ^(0[xX])?[A-Fa-f0-9]{64}(,(0[xX])?[A-Fa-f0-9]{64})*$ ]]
}

node_dir_for() {
  printf '%s/%s' "$NODES_DIR" "$1"
}

compose_for() {
  local name="$1"
  local dir
  dir="$(node_dir_for "$name")"
  [[ -f "${dir}/compose.yaml" ]] || die "node '$name' is not configured"
  docker compose --project-name "gyds-${name}" --project-directory "$dir" -f "${dir}/compose.yaml" "${@:2}"
}

port_reserved_by_fleet() {
  local port="$1" env_file
  for env_file in "${NODES_DIR}"/*/node.env; do
    [[ -f "$env_file" ]] || continue
    if grep -Eq "^GYDS_(P2P_PORT|RPC_PORT|DASHBOARD_PORT)=${port}$" "$env_file"; then
      return 0
    fi
  done
  return 1
}

port_is_listening() {
  bash -c "echo >/dev/tcp/127.0.0.1/$1" >/dev/null 2>&1
}

find_port() {
  local start="$1" end="$2" port
  for ((port = start; port <= end; port++)); do
    if ! port_reserved_by_fleet "$port" && ! port_is_listening "$port"; then
      printf '%s' "$port"
      return 0
    fi
  done
  return 1
}

add_node() {
  (($# >= 3 && $# <= 6)) || die "usage: add NAME MODE NETWORK [BOOTSTRAP_ADDRESSES] [ALLOWED_PEER_IDS] [ADVERTISE_HOST]"
  local name="$1" mode="$2" network="$3"
  local bootstrap="${4:-}" allowed_ids="${5:-}" advertise_host="${6:-}"
  valid_name "$name" || die "NAME must start with a lowercase letter and contain only lowercase letters, digits, '_' or '-'"
  case "$mode" in
    full|lite|sync|boost) ;;
    rpc) die "rpc mode has no peer-height source; use full or sync mode to verify catch-up before readiness" ;;
    *) die "unsupported mode '$mode'; use full, lite, sync, or boost" ;;
  esac
  case "$network" in
    mainnet|testnet|testnet3b) ;;
    *) die "unsupported network '$network'; use mainnet, testnet, or testnet3b" ;;
  esac
  [[ ! -e "$(node_dir_for "$name")" ]] || die "node '$name' already exists"
  if [[ -n "$bootstrap" ]]; then
    validate_bootstrap_list "$bootstrap" || die "invalid bootstrap list; use comma-separated host:port addresses"
    [[ -n "$allowed_ids" ]] || die "P2P bootstrap peers require their IDs in ALLOWED_PEER_IDS"
  fi
  if [[ -n "$allowed_ids" ]]; then
    validate_node_ids "$allowed_ids" || die "peer IDs must be 64-digit hexadecimal IDs, comma-separated"
  fi
  [[ -n "$bootstrap" ]] || die "$mode mode requires at least one bootstrap address"
  [[ -n "$allowed_ids" ]] || die "$mode mode requires trusted peer IDs"
  if [[ "$mode" == rpc && ( -n "$bootstrap" || -n "$allowed_ids" ) ]]; then
    die "rpc mode has no P2P; omit bootstrap addresses and peer IDs"
  fi
  if [[ -n "$advertise_host" && ! "$advertise_host" =~ ^[A-Za-z0-9._:-]+$ ]]; then
    die "ADVERTISE_HOST may contain only letters, digits, '.', '-', '_', ':'"
  fi

  require_docker
  mkdir -p "$NODES_DIR"
  printf 'Building %s from %s...\n' "$IMAGE" "$REPO_ROOT"
  docker build --tag "$IMAGE" "$REPO_ROOT"

  local p2p_port rpc_port dashboard_port dir project volume memory_limit cpu_limit
  p2p_port="$(find_port 30303 30999)" || die "no free P2P ports in range 30303-30999"
  rpc_port="$(find_port 18545 19999)" || die "no free RPC ports in range 18545-19999"
  dashboard_port="$(find_port 15000 16999)" || die "no free dashboard ports in range 15000-16999"
  case "$mode" in
    lite) memory_limit="512m"; cpu_limit="0.5" ;;
    rpc) memory_limit="1g"; cpu_limit="1.0" ;;
    *) memory_limit="2g"; cpu_limit="2.0" ;;
  esac
  dir="$(node_dir_for "$name")"
  project="gyds-${name}"
  volume="gyds-${name}-data"
  mkdir -m 0700 -p "$dir"

  umask 077
  cat > "${dir}/node.env" <<EOF
GYDS_NETWORK=${network}
GYDS_NODE_MODE=${mode}
GYDS_DATA_DIR=/app/data
GYDS_P2P_PORT=${p2p_port}
GYDS_RPC_PORT=${rpc_port}
GYDS_DASHBOARD_PORT=${dashboard_port}
GYDS_RPC_HOST=0.0.0.0
GYDS_PEER_AUTH=true
GYDS_BOOTSTRAP_NODES=${bootstrap}
GYDS_ALLOWED_NODES=${allowed_ids}
EOF
  if [[ -n "$advertise_host" ]]; then
    printf 'GYDS_P2P_ADVERTISE_HOST=%s\n' "$advertise_host" >> "${dir}/node.env"
  fi
  chmod 0600 "${dir}/node.env"

  cat > "${dir}/compose.yaml" <<EOF
services:
  node:
    image: ${IMAGE}
    container_name: ${project}
    restart: unless-stopped
    user: "gyds:gyds"
    env_file:
      - ${dir}/node.env
    ports:
EOF
  if [[ "$mode" != rpc ]]; then
    printf '      - "0.0.0.0:%s:%s/tcp"\n' "$p2p_port" "$p2p_port" >> "${dir}/compose.yaml"
  fi
  if [[ "$mode" != lite ]]; then
    printf '      - "127.0.0.1:%s:%s/tcp"\n' "$dashboard_port" "$dashboard_port" >> "${dir}/compose.yaml"
  fi
  cat >> "${dir}/compose.yaml" <<EOF
      - "127.0.0.1:${rpc_port}:${rpc_port}/tcp"
    volumes:
      - node-data:/app/data
    mem_limit: ${memory_limit}
    cpus: ${cpu_limit}
    logging:
      driver: json-file
      options:
        max-size: "100m"
        max-file: "5"
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://127.0.0.1:${rpc_port}/ready || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 5
      start_period: 30s

volumes:
  node-data:
    name: ${volume}
EOF
  chmod 0600 "${dir}/compose.yaml"

  docker compose --project-name "$project" --project-directory "$dir" -f "${dir}/compose.yaml" up -d
  cat <<EOF

Node '$name' started in sync-only state. It becomes healthy only after /ready
returns HTTP 200; state RPC calls and the main dashboard remain gated meanwhile.
  Mode/network: ${mode}/${network}
  P2P TCP:      ${p2p_port} (public)
  RPC:          127.0.0.1:${rpc_port}
  Dashboard:    $([[ "$mode" == lite ]] && printf 'disabled' || printf '127.0.0.1:%s' "$dashboard_port")
  Data volume:  ${volume} (kept unless explicitly deleted)
  Config:       ${dir}/node.env

Use 'scripts/docker-node-fleet.sh status ${name}' to check it.
EOF
  if command -v curl >/dev/null 2>&1; then
    curl --max-time 3 -sS "http://127.0.0.1:${rpc_port}/p2p/node-id" || true
    printf '\n'
    curl --max-time 3 -sS "http://127.0.0.1:${rpc_port}/sync-status" || true
    printf '\n'
    curl --max-time 3 -sS "http://127.0.0.1:${rpc_port}/ready" || true
    printf '\n'
  fi
}

list_nodes() {
  mkdir -p "$NODES_DIR"
  local dir name
  shopt -s nullglob
  local found=0
  for dir in "${NODES_DIR}"/*; do
    [[ -d "$dir" && -f "${dir}/node.env" ]] || continue
    found=1
    name="${dir##*/}"
    printf '\n%s\n' "$name"
    grep -E '^GYDS_(NETWORK|NODE_MODE|P2P_PORT|RPC_PORT|DASHBOARD_PORT)=' "${dir}/node.env" || true
    compose_for "$name" ps --format table
  done
  ((found)) || printf 'No fleet nodes configured under %s\n' "$NODES_DIR"
}

delete_node() {
  (($# == 3)) || die "deleting a node requires: delete NAME --delete-data --yes"
  local name="$1"
  [[ "$2" == "--delete-data" && "$3" == "--yes" ]] ||
    die "data deletion requires both --delete-data and --yes"
  valid_name "$name" || die "invalid node name"
  local dir volume
  dir="$(node_dir_for "$name")"
  [[ -f "${dir}/compose.yaml" ]] || die "node '$name' is not configured"
  require_docker
  volume="gyds-${name}-data"
  printf 'Removing the container and permanently deleting volume %s...\n' "$volume"
  compose_for "$name" down --volumes --remove-orphans
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf -- "$dir"
  printf "Node '%s' and its stored data have been deleted.\n" "$name"
}

command="${1:-}"
case "$command" in
  add)
    shift
    add_node "$@"
    ;;
  list)
    list_nodes
    ;;
  start|stop|status|logs)
    (($# == 2)) || die "usage: $command NAME"
    name="$2"
    valid_name "$name" || die "invalid node name"
    require_docker
    case "$command" in
      start) compose_for "$name" up -d ;;
      stop) compose_for "$name" stop ;;
      status)
        compose_for "$name" ps
        env_file="$(node_dir_for "$name")/node.env"
        rpc_port="$(sed -n 's/^GYDS_RPC_PORT=//p' "$env_file")"
        if command -v curl >/dev/null 2>&1; then
          curl --max-time 3 -fsS "http://127.0.0.1:${rpc_port}/p2p/node-id" || true
          printf '\n'
          curl --max-time 3 -fsS "http://127.0.0.1:${rpc_port}/sync-status" || true
          printf '\n'
          curl --max-time 3 -fsS "http://127.0.0.1:${rpc_port}/ready" || true
          printf '\n'
        fi
        ;;
      logs) compose_for "$name" logs --tail=100 --follow ;;
    esac
    ;;
  delete)
    shift
    delete_node "$@"
    ;;
  --help|-h|help|"")
    usage
    ;;
  *)
    die "unknown command '$command'"
    ;;
esac
