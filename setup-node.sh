#!/usr/bin/env bash
# Interactive node profile generator.
# Generates one protected .env-compatible file per run; it never starts a node,
# installs packages, or creates/deletes chain data.
set -euo pipefail

usage() {
  cat <<'EOF'
GYDS node setup

Usage:
  bash setup-node.sh [--output FILE]

Choose one supported role per run:
  full, lite, rpc, boost, genesis, sync, validator, testnode

The generated file is separate from .env by default and has mode 0600.
Run this script again to create another node profile.
EOF
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

prompt_value() {
  local name="$1" label="$2" default="$3" answer
  read -r -p "$label [$default]: " answer
  printf -v "$name" '%s' "${answer:-$default}"
}

prompt_optional() {
  local name="$1" label="$2" answer
  read -r -p "$label (leave blank if not needed): " answer
  printf -v "$name" '%s' "$answer"
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

validate_bootstrap_list() {
  local input="$1" item hostport port
  local -a peers
  IFS=',' read -r -a peers <<< "$input"
  for item in "${peers[@]}"; do
    item="${item//[[:space:]]/}"
    item="${item#tcp://}"
    [[ "$item" =~ ^(\[[0-9a-fA-F:]+\]|[A-Za-z0-9._-]+):[0-9]+$ ]] ||
      die "invalid bootstrap peer '$item'; use host:port or [IPv6]:port"
    hostport="${item##*:}"
    port="$hostport"
    validate_port "Bootstrap peer port" "$port"
  done
}

normalize_node_ids() {
  local input="$1" item result="" normalized
  local -a ids
  [[ -z "${input//[[:space:]]/}" ]] && { printf ''; return; }
  IFS=',' read -r -a ids <<< "$input"
  for item in "${ids[@]}"; do
    item="${item//[[:space:]]/}"
    normalized="${item,,}"
    normalized="${normalized#0x}"
    [[ "$normalized" =~ ^[0-9a-f]{64}$ ]] ||
      die "invalid approved node ID '$item'; each ID must be 64 hex characters"
    [[ -z "$result" ]] || result+=","
    result+="$normalized"
  done
  printf '%s' "$result"
}

emit() {
  printf '%s=%q\n' "$1" "$2"
}

MODE=""
OUTPUT_OVERRIDE=""
while (($#)); do
  case "$1" in
    --output)
      (($# >= 2)) || die "--output requires a file path"
      OUTPUT_OVERRIDE="$2"
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

printf 'Choose the node role to configure:\n'
printf '  1) full       — complete node with P2P\n'
printf '  2) lite       — lightweight P2P node\n'
printf '  3) rpc        — API/dashboard only, no P2P\n'
printf '  4) boost      — high-throughput P2P node\n'
printf '  5) genesis    — initial network/bootstrap node\n'
printf '  6) sync       — catch up from trusted P2P peers\n'
printf '  7) validator  — PoS node with optional dedicated signing key\n'
printf '  8) testnode   — isolated disposable development node\n'
read -r -p "Role [1]: " mode_choice
mode_choice="${mode_choice:-1}"
case "$mode_choice" in
  1|full) MODE=full ;;
  2|lite) MODE=lite ;;
  3|rpc) MODE=rpc ;;
  4|boost) MODE=boost ;;
  5|genesis) MODE=genesis ;;
  6|sync) MODE=sync ;;
  7|validator) MODE=validator ;;
  8|testnode) MODE=testnode ;;
  *) die "select a role from 1 to 8, or enter its name" ;;
esac

if [[ "$MODE" == "testnode" ]]; then
  cat <<'EOF'

WARNING: testnode is isolated and clears its own <data directory>/test folder
on every start. Never point it at data that must be preserved.
EOF
  read -r -p "Type TESTNODE to confirm this disposable role: " typed_testnode
  [[ "$typed_testnode" == "TESTNODE" ]] || die "testnode setup cancelled"
  NETWORK="testnode"
  CHAIN_ID=31337
  NETWORK_NAME="GYDS Test Network"
  DATA_DEFAULT="/var/lib/gyds-testnode"
else
  printf '\nChoose a network profile:\n'
  printf '  1) mainnet    — chain ID 198282\n'
  printf '  2) testnet    — existing testnet, chain ID 198281\n'
  printf '  3) testnet3b  — separate testnet, chain ID 198284\n'
  read -r -p "Network [1]: " network_choice
  network_choice="${network_choice:-1}"
  case "$network_choice" in
    1|mainnet)
      NETWORK=mainnet
      CHAIN_ID=198282
      NETWORK_NAME="GYDS Chain"
      DATA_DEFAULT="/var/lib/gyds-fullnode"
      ;;
    2|testnet)
      NETWORK=testnet
      CHAIN_ID=198281
      NETWORK_NAME="GYDS Testnet"
      DATA_DEFAULT="/var/lib/gyds-fullnode-testnet"
      ;;
    3|testnet3b)
      NETWORK=testnet3b
      CHAIN_ID=198284
      NETWORK_NAME="GYDS Testnet 3B"
      DATA_DEFAULT="/var/lib/gyds-fullnode-testnet3b"
      ;;
    *) die "select a network from 1 to 3, or enter its name" ;;
  esac
fi

if [[ "$MODE" == "genesis" && "$NETWORK" == "mainnet" ]]; then
  cat <<'EOF'

CAUTION: a genesis-role profile is for the initial node in the existing
mainnet. It does not reset data, but do not use it to create a replacement
chain or to overwrite an existing node configuration.
EOF
  read -r -p "Type MAINNET-GENESIS to continue: " typed_genesis
  [[ "$typed_genesis" == "MAINNET-GENESIS" ]] || die "mainnet genesis profile cancelled"
fi

case "$MODE" in
  full)      PORT_DEFAULTS=(5000 8545 8546 30303) ;;
  lite)      PORT_DEFAULTS=(5010 8555 8556 30313) ;;
  rpc)       PORT_DEFAULTS=(5020 8565 8566 30323) ;;
  boost)     PORT_DEFAULTS=(5030 8575 8576 30333) ;;
  genesis)   PORT_DEFAULTS=(5040 8585 8586 30343) ;;
  sync)      PORT_DEFAULTS=(5050 8595 8596 30353) ;;
  validator) PORT_DEFAULTS=(5060 8605 8606 30363) ;;
  testnode)  PORT_DEFAULTS=(15000 18545 18546 31337) ;;
esac

printf '\nEach node must use its own data directory and node key.\n'
if [[ "$NETWORK" == "mainnet" ]]; then
  printf 'For an existing mainnet node, enter its current data directory exactly; do not point a testnet database at mainnet.\n'
fi
prompt_value DATA_DIR "Node data directory" "$DATA_DEFAULT"
[[ -n "$DATA_DIR" && "$DATA_DIR" != *$'\n'* && "$DATA_DIR" != *$'\r'* ]] ||
  die "data directory must be a non-empty single-line path"

if [[ "$MODE" == "testnode" ]]; then
  DASHBOARD_PORT=15000
  RPC_PORT=18545
  WS_PORT=18546
  P2P_PORT=31337
  printf 'Testnode ports are fixed: dashboard=15000, RPC=18545, WebSocket=18546, P2P=31337.\n'
else
  prompt_value DASHBOARD_PORT "Dashboard port" "${PORT_DEFAULTS[0]}"
  prompt_value RPC_PORT "JSON-RPC port" "${PORT_DEFAULTS[1]}"
  prompt_value WS_PORT "WebSocket port" "${PORT_DEFAULTS[2]}"
  prompt_value P2P_PORT "P2P port" "${PORT_DEFAULTS[3]}"
  validate_port "Dashboard port" "$DASHBOARD_PORT"
  validate_port "JSON-RPC port" "$RPC_PORT"
  validate_port "WebSocket port" "$WS_PORT"
  validate_port "P2P port" "$P2P_PORT"
  if [[ "$DASHBOARD_PORT" == "$RPC_PORT" || "$DASHBOARD_PORT" == "$WS_PORT" ||
        "$DASHBOARD_PORT" == "$P2P_PORT" || "$RPC_PORT" == "$WS_PORT" ||
        "$RPC_PORT" == "$P2P_PORT" || "$WS_PORT" == "$P2P_PORT" ]]; then
    die "dashboard, RPC, WebSocket, and P2P ports must be different"
  fi
fi

if [[ "$MODE" == "testnode" ]]; then
  RPC_HOST=127.0.0.1
else
  prompt_value RPC_HOST "RPC bind host (127.0.0.1 is safer)" "127.0.0.1"
fi
PUBLIC_RPC_DEFAULT=""
if [[ "$NETWORK" == "mainnet" && "$MODE" != "testnode" ]]; then
  PUBLIC_RPC_DEFAULT="https://app.netlifegy.com/api/rpc?network=mainnet"
fi
prompt_value PUBLIC_RPC_URL "Public wallet-facing JSON-RPC URL" "$PUBLIC_RPC_DEFAULT"
if [[ -n "$PUBLIC_RPC_URL" && ! "$PUBLIC_RPC_URL" =~ ^https://[^[:space:]]+$ ]]; then
  die "public RPC URL must be HTTPS and contain no whitespace"
fi
prompt_yes_no DASHBOARD_ENABLED "Enable the dashboard" "true"
if [[ "$MODE" == "testnode" ]]; then
  BLOCK_TIME=5
  MAX_PEERS=0
  printf 'Testnode block time is fixed at 5 seconds and P2P is disabled.\n'
elif [[ "$MODE" == "rpc" ]]; then
  prompt_value BLOCK_TIME "Block interval in seconds" "120"
  [[ "$BLOCK_TIME" =~ ^[0-9]+$ ]] || die "block interval must be a whole number of seconds"
  ((10#$BLOCK_TIME >= 1 && 10#$BLOCK_TIME <= 3600)) || die "block interval must be between 1 and 3600 seconds"
  MAX_PEERS=0
else
  prompt_value BLOCK_TIME "Block interval in seconds" "120"
  [[ "$BLOCK_TIME" =~ ^[0-9]+$ ]] || die "block interval must be a whole number of seconds"
  ((10#$BLOCK_TIME >= 1 && 10#$BLOCK_TIME <= 3600)) || die "block interval must be between 1 and 3600 seconds"
  prompt_value MAX_PEERS "Maximum P2P peers" "25"
  [[ "$MAX_PEERS" =~ ^[0-9]+$ ]] && ((10#$MAX_PEERS >= 1 && 10#$MAX_PEERS <= 10000)) ||
    die "maximum peers must be between 1 and 10000"
fi
prompt_value LOG_LEVEL "Log level (trace/debug/info/warn/error)" "info"
case "$LOG_LEVEL" in trace|debug|info|warn|error) ;; *) die "unsupported log level" ;; esac
prompt_value STORAGE_LIMIT "Storage limit in GB" "50"
[[ "$STORAGE_LIMIT" =~ ^[0-9]+$ ]] && ((10#$STORAGE_LIMIT >= 1)) || die "storage limit must be a positive whole number"

BOOTSTRAP_NODES=""
ALLOWED_NODES=""
PEER_AUTH=false
P2P_ADVERTISE_HOST=""
ALLOW_PRIVATE_ADVERTISE=false
PEER_REGISTRATION_URL=""
VALIDATOR_KEY=""

case "$MODE" in
  full|lite|boost|genesis|sync|validator)
    PEER_AUTH=true
    prompt_value P2P_ADVERTISE_HOST "This node's P2P address reachable by peers (for WireGuard, this node's own VPN IP)" ""
    [[ -n "$P2P_ADVERTISE_HOST" && "$P2P_ADVERTISE_HOST" != *[[:space:]]* ]] ||
      die "a reachable P2P advertise host is required for $MODE mode"
    if [[ "$P2P_ADVERTISE_HOST" =~ ^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.) ]]; then
      prompt_yes_no ALLOW_PRIVATE_ADVERTISE "Allow this private/VPN advertise address" "true"
    fi

    if [[ "$MODE" != "genesis" ]]; then
      prompt_value BOOTSTRAP_NODES "Bootstrap peers (comma-separated host:port)" ""
      [[ -n "$BOOTSTRAP_NODES" ]] || die "$MODE mode needs at least one bootstrap peer"
      validate_bootstrap_list "$BOOTSTRAP_NODES"
    else
      prompt_optional BOOTSTRAP_NODES "Optional bootstrap peers"
      [[ -z "$BOOTSTRAP_NODES" ]] || validate_bootstrap_list "$BOOTSTRAP_NODES"
    fi

    if [[ "$MODE" == "genesis" ]]; then
      prompt_optional ALLOWED_NODES "Trusted node IDs (blank denies all inbound peers)"
    else
      prompt_value ALLOWED_NODES "Trusted peer node IDs (comma-separated 64-character hex IDs)" ""
      [[ -n "$ALLOWED_NODES" ]] || die "$MODE mode needs at least one trusted peer ID"
    fi
    ALLOWED_NODES="$(normalize_node_ids "$ALLOWED_NODES")"
    prompt_optional PEER_REGISTRATION_URL "Optional enrollment coordinator URL (for example http://10.0.0.1:5000)"
    if [[ -n "$PEER_REGISTRATION_URL" && ! "$PEER_REGISTRATION_URL" =~ ^https?://[^[:space:]]+$ ]]; then
      die "enrollment coordinator URL must start with http:// or https://"
    fi
    ;;
  rpc|testnode)
    printf 'This role does not use P2P; bootstrap and peer-ID settings will be empty.\n'
    ;;
esac

if [[ "$MODE" == "validator" ]]; then
  cat <<'EOF'

Validator key warning: a supplied private key is stored in the generated .env
file. The file will be mode 0600, but the key is not encrypted. Use only a
dedicated validator key; never enter an Admin, personal, or treasury key.
EOF
  read -r -s -p "Optional validator signing key (hex, input hidden; Enter to skip): " VALIDATOR_KEY
  printf '\n'
  if [[ -n "$VALIDATOR_KEY" ]]; then
    VALIDATOR_KEY="${VALIDATOR_KEY,,}"
    VALIDATOR_KEY="${VALIDATOR_KEY#0x}"
    [[ "$VALIDATOR_KEY" =~ ^[0-9a-f]{64}$ ]] || die "validator key must be 64 hexadecimal characters"
    VALIDATOR_KEY="0x$VALIDATOR_KEY"
    read -r -p "Save this key in the protected generated .env file? [y/N]: " save_key
    [[ "${save_key,,}" == "y" || "${save_key,,}" == "yes" ]] ||
      die "validator setup cancelled; no config was written"
  else
    printf 'No dedicated validator key supplied; this role will use the node default behavior.\n'
  fi
fi

if [[ "$MODE" == "testnode" ]]; then
  ENABLE_FIREWALL=false
  ENABLE_FAIL2BAN=false
  printf 'Testnode uses loopback-only listeners; installer firewall changes are disabled.\n'
else
  prompt_yes_no ENABLE_FIREWALL "Enable installer firewall configuration" "true"
  prompt_yes_no ENABLE_FAIL2BAN "Enable optional Fail2ban setup" "true"
fi
prompt_value SSH_PORT "SSH port" "22"
validate_port "SSH port" "$SSH_PORT"
prompt_optional ALLOWED_IPS "Optional comma-separated admin/VPN IPs for installer firewall rules"
if [[ "$MODE" == "testnode" ]]; then
  AUTO_UPDATES=false
else
  prompt_yes_no AUTO_UPDATES "Enable automatic node release checks" "true"
fi

if [[ -n "$OUTPUT_OVERRIDE" ]]; then
  OUTPUT_FILE="$OUTPUT_OVERRIDE"
elif [[ "$MODE" == "testnode" ]]; then
  OUTPUT_FILE=".env.generated-testnode"
else
  OUTPUT_FILE=".env.generated-${NETWORK}-${MODE}"
fi
prompt_value OUTPUT_FILE "Generated config path" "$OUTPUT_FILE"
[[ -n "$OUTPUT_FILE" && "$OUTPUT_FILE" != *$'\n'* && "$OUTPUT_FILE" != *$'\r'* ]] ||
  die "output path must be a non-empty single-line path"
case "$(basename -- "$OUTPUT_FILE")" in
  .env.example|.env.testnode|*.env.example)
    die "refusing to overwrite a checked-in example environment file"
    ;;
esac
[[ ! -L "$OUTPUT_FILE" ]] || die "refusing to replace a symbolic link: $OUTPUT_FILE"

if [[ -e "$OUTPUT_FILE" ]]; then
  printf '\nThe output file already exists: %s\n' "$OUTPUT_FILE"
  read -r -p "Type OVERWRITE to back it up and replace it: " overwrite
  [[ "$overwrite" == "OVERWRITE" ]] || die "configuration generation cancelled"
  backup_file="${OUTPUT_FILE}.bak.$(date -u +%Y%m%dT%H%M%SZ).$$"
  cp -p -- "$OUTPUT_FILE" "$backup_file"
  chmod 600 "$backup_file"
fi

output_directory="$(dirname -- "$OUTPUT_FILE")"
mkdir -p -- "$output_directory"
tmp_file="$(mktemp -- "${OUTPUT_FILE}.tmp.XXXXXX")"
trap '[[ -e "${tmp_file:-}" ]] && rm -f -- "$tmp_file"' EXIT

{
  printf '# Generated by setup-node.sh; review before installation.\n'
  printf '# This file does not contain an Admin PIN or wallet private key.\n'
  if [[ "$NETWORK" == "testnode" ]]; then
    emit GYDS_NETWORK ""
    emit GYDS_CHAIN_ID "$CHAIN_ID"
    emit GYDS_NETWORK_NAME "$NETWORK_NAME"
  else
    emit GYDS_NETWORK "$NETWORK"
    emit GYDS_CHAIN_ID "$CHAIN_ID"
    emit GYDS_NETWORK_NAME "$NETWORK_NAME"
  fi
  emit GYDS_NODE_MODE "$MODE"
  emit GYDS_DATA_DIR "$DATA_DIR"
  emit GYDS_BLOCK_TIME "$BLOCK_TIME"
  emit GYDS_DASHBOARD_ENABLED "$DASHBOARD_ENABLED"
  emit GYDS_DASHBOARD_PORT "$DASHBOARD_PORT"
  emit GYDS_RPC_HOST "$RPC_HOST"
  emit GYDS_RPC_PORT "$RPC_PORT"
  emit GYDS_PUBLIC_RPC_URL "$PUBLIC_RPC_URL"
  emit GYDS_WS_PORT "$WS_PORT"
  emit GYDS_P2P_PORT "$P2P_PORT"
  emit GYDS_MAX_PEERS "$MAX_PEERS"
  emit GYDS_PEER_AUTH "$PEER_AUTH"
  emit GYDS_P2P_ADVERTISE_HOST "$P2P_ADVERTISE_HOST"
  emit GYDS_P2P_ALLOW_PRIVATE_ADVERTISE "$ALLOW_PRIVATE_ADVERTISE"
  emit GYDS_BOOTSTRAP_NODES "$BOOTSTRAP_NODES"
  emit GYDS_ALLOWED_NODES "$ALLOWED_NODES"
  emit GYDS_PEER_REGISTRATION_URL "$PEER_REGISTRATION_URL"
  emit GYDS_AUTO_NODE_UPDATES "$AUTO_UPDATES"
  emit GYDS_ENABLE_FIREWALL "$ENABLE_FIREWALL"
  emit GYDS_ENABLE_FAIL2BAN "$ENABLE_FAIL2BAN"
  emit GYDS_SSH_PORT "$SSH_PORT"
  emit GYDS_ALLOWED_IPS "$ALLOWED_IPS"
  emit GYDS_STORAGE_LIMIT_GB "$STORAGE_LIMIT"
  emit GYDS_LOG_LEVEL "$LOG_LEVEL"
  emit GYDS_LOG_FORMAT "json"
  if [[ "$MODE" == "testnode" ]]; then
    emit GYDS_TESTNODE_DASHBOARD_PORT "$DASHBOARD_PORT"
    emit GYDS_TESTNODE_RPC_PORT "$RPC_PORT"
    emit GYDS_TESTNODE_WS_PORT "$WS_PORT"
    emit GYDS_TESTNODE_P2P_PORT "$P2P_PORT"
  fi
  if [[ "$MODE" == "validator" && -n "$VALIDATOR_KEY" ]]; then
    emit GYDS_VALIDATOR_KEY "$VALIDATOR_KEY"
  else
    printf '# GYDS_VALIDATOR_KEY=\n'
  fi
} > "$tmp_file"

chmod 600 "$tmp_file"
mv -- "$tmp_file" "$OUTPUT_FILE"
tmp_file=""

printf '\nGenerated node configuration: %s\n' "$OUTPUT_FILE"
printf '  Network: %s (chain ID %s)\n' "$NETWORK" "$CHAIN_ID"
printf '  Role:    %s\n' "$MODE"
printf '  Data:    %s\n' "$DATA_DIR"
printf '  Mode:    permissions 0600; no installation or service restart was run\n'
if [[ "$MODE" == "testnode" ]]; then
  printf '  WARNING: its %s/test data directory is cleared every start.\n' "$DATA_DIR"
elif [[ "$MODE" != "rpc" && -z "$ALLOWED_NODES" ]]; then
  printf '  WARNING: empty trusted-node list means inbound peers are rejected.\n'
fi
printf '\nReview the generated file before installing. For a new server only:\n'
printf '  sudo bash deploy.sh --env %q\n' "$OUTPUT_FILE"
printf 'Do not use that command to replace an existing mainnet configuration without reviewing its data directory and current role.\n'
