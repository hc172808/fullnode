#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
#  GYDS Chain — Production Deploy Script
#
#  Usage:
#    bash deploy.sh [OPTIONS]
#
#  Options:
#    --env FILE        Path to .env file (default: .env)
#    --no-firewall     Skip firewall configuration
#    --no-service      Skip systemd service setup (run in foreground)
#    --update          Update an existing deployment (rebuild + restart)
#    --uninstall       Remove GYDS fullnode service, binary, and data
#    --status          Show current service status and chain health
#    --help            Show this help message
#
#  Prerequisites:
#    - Go 1.21+
#    - systemd (for service management)
#    - ufw + fail2ban (for firewall, optional with --no-firewall)
#    - A filled-in .env file  (copy .env.example → .env and edit)
#      OR run the web setup wizard first at http://<host>:<rpc_port>/setup
# ══════════════════════════════════════════════════════════════════════════════
set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; YELLOW='\033[1;33m'; GREEN='\033[0;32m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

log()     { echo -e "${GREEN}[$(date '+%H:%M:%S')]${NC} $*"; }
info()    { echo -e "${CYAN}[$(date '+%H:%M:%S')]${NC} $*"; }
warn()    { echo -e "${YELLOW}[$(date '+%H:%M:%S')] WARNING:${NC} $*"; }
error()   { echo -e "${RED}[$(date '+%H:%M:%S')] ERROR:${NC} $*" >&2; }
step()    { echo -e "\n${BOLD}${CYAN}━━━  $*  ━━━${NC}\n"; }
banner()  {
  echo -e "${CYAN}"
  echo "  ██████╗ ██╗   ██╗██████╗ ███████╗"
  echo "  ██╔════╝╚██╗ ██╔╝██╔══██╗██╔════╝"
  echo "  ██║  ███╗╚████╔╝ ██║  ██║███████╗"
  echo "  ██║   ██║ ╚██╔╝  ██║  ██║╚════██║"
  echo "  ╚██████╔╝  ██║   ██████╔╝███████║"
  echo "   ╚═════╝   ╚═╝   ╚═════╝ ╚══════╝"
  echo -e "  ${BOLD}GYDS Chain Full Node — Production Deploy${NC}${CYAN}"
  echo "══════════════════════════════════════════"
  echo -e "${NC}"
}

# ── Defaults ─────────────────────────────────────────────────────────────────
ENV_FILE=".env"
SKIP_FIREWALL=false
SKIP_SERVICE=false
IS_UPDATE=false
IS_REBUILD=false
UNINSTALL=false
SHOW_STATUS=false

APP_NAME="gyds-fullnode"
APP_USER="gyds"
APP_GROUP="gyds"
INSTALL_DIR="/opt/gyds-fullnode"
BINARY_PATH="/usr/local/bin/gyds-fullnode"
SERVICE_FILE="/etc/systemd/system/gyds-fullnode.service"
LOG_DIR="/var/log/gyds-fullnode"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Argument parsing ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --env)           ENV_FILE="$2"; shift 2 ;;
    --no-firewall)   SKIP_FIREWALL=true; shift ;;
    --no-service)    SKIP_SERVICE=true; shift ;;
    --update)        IS_UPDATE=true; shift ;;
    --rebuild)       IS_REBUILD=true; IS_UPDATE=true; shift ;;
    --uninstall)     UNINSTALL=true; shift ;;
    --status)        SHOW_STATUS=true; shift ;;
    --help|-h)
      echo "Usage: bash deploy.sh [OPTIONS]"
      echo ""
      echo "  --env FILE      Path to .env file (default: .env)"
      echo "  --no-firewall   Skip firewall configuration"
      echo "  --no-service    Run in foreground instead of systemd"
      echo "  --update        Rebuild binary + restart service (preserves data)"
      echo "  --rebuild       Full clean rebuild: wipe binary + caches, re-build"
      echo "  --uninstall     Remove service, binary, and install files"
      echo "  --status        Show service status and chain health"
      echo ""
      echo "Safe to run multiple times — skips steps already done."
      exit 0 ;;
    *) shift ;;
  esac
done

banner

# ── Status ────────────────────────────────────────────────────────────────────
if $SHOW_STATUS; then
  step "Service Status"
  systemctl status gyds-fullnode --no-pager 2>/dev/null || echo "Service not installed"
  echo ""
  RPC_PORT=$(grep "^GYDS_RPC_PORT" "${ENV_FILE}" 2>/dev/null | cut -d= -f2 | tr -d ' ' || echo "8545")
  info "Health check:"
  curl -sf "http://localhost:${RPC_PORT}/health" | python3 -m json.tool 2>/dev/null \
    || curl -sf "http://localhost:${RPC_PORT}/health" || echo "Node not responding"
  exit 0
fi

# ── Uninstall ─────────────────────────────────────────────────────────────────
if $UNINSTALL; then
  step "Uninstalling GYDS Fullnode"
  read -rp "$(echo -e "${RED}This will stop the service and remove all files. Continue? [y/N]: ${NC}")" confirm
  [[ "${confirm,,}" != "y" ]] && { info "Aborted."; exit 0; }

  systemctl stop  gyds-fullnode 2>/dev/null || true
  systemctl disable gyds-fullnode 2>/dev/null || true
  rm -f  "$SERVICE_FILE"
  rm -f  "$BINARY_PATH"
  rm -rf "$INSTALL_DIR"
  rm -rf "$LOG_DIR"
  systemctl daemon-reload 2>/dev/null || true
  log "Uninstall complete. Chain data in /var/lib/gyds-fullnode was preserved."
  log "To remove chain data: sudo rm -rf /var/lib/gyds-fullnode"
  exit 0
fi

# ── Load .env ─────────────────────────────────────────────────────────────────
step "Loading Configuration"

if [[ ! -f "$ENV_FILE" ]]; then
  error ".env file not found: $ENV_FILE"
  echo ""
  echo "  Option 1 — copy the example and fill in your values:"
  echo "    cp .env.example .env && nano .env"
  echo ""
  echo "  Option 2 — use the web setup wizard:"
  echo "    go run . start"
  echo "    Then open: http://localhost:8545/setup"
  echo ""
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

# Apply defaults for anything not in .env
GYDS_CHAIN_ID="${GYDS_CHAIN_ID:-13370}"
GYDS_NODE_MODE="${GYDS_NODE_MODE:-full}"
GYDS_RPC_PORT="${GYDS_RPC_PORT:-8545}"
GYDS_WS_PORT="${GYDS_WS_PORT:-8546}"
GYDS_P2P_PORT="${GYDS_P2P_PORT:-30303}"
GYDS_RPC_HOST="${GYDS_RPC_HOST:-0.0.0.0}"
GYDS_DATA_DIR="${GYDS_DATA_DIR:-/var/lib/gyds-fullnode}"
GYDS_LOG_LEVEL="${GYDS_LOG_LEVEL:-info}"
GYDS_LOG_FORMAT="${GYDS_LOG_FORMAT:-json}"
GYDS_SSH_PORT="${GYDS_SSH_PORT:-22}"
GYDS_STORAGE_LIMIT_GB="${GYDS_STORAGE_LIMIT_GB:-50}"
GYDS_ENABLE_FIREWALL="${GYDS_ENABLE_FIREWALL:-true}"

log "Chain ID       : $GYDS_CHAIN_ID"
log "Node mode      : $GYDS_NODE_MODE"
log "RPC port       : $GYDS_RPC_PORT"
log "P2P port       : $GYDS_P2P_PORT"
log "Data directory : $GYDS_DATA_DIR"
log "Storage limit  : ${GYDS_STORAGE_LIMIT_GB} GB"

# ── Prerequisite checks ───────────────────────────────────────────────────────
step "Checking Prerequisites"

check_cmd() {
  if ! command -v "$1" &>/dev/null; then
    error "Required tool not found: $1  ($2)"
    exit 1
  fi
  log "✓ $1"
}

check_cmd go  "Install from https://go.dev/dl/"
check_cmd git "sudo apt install git"

GO_VER=$(go version | awk '{print $3}' | sed 's/go//')
GO_MAJOR=$(echo "$GO_VER" | cut -d. -f1)
GO_MINOR=$(echo "$GO_VER" | cut -d. -f2)
if [[ "$GO_MAJOR" -lt 1 || ("$GO_MAJOR" -eq 1 && "$GO_MINOR" -lt 21) ]]; then
  error "Go 1.21+ required (found $GO_VER). Install from https://go.dev/dl/"
  exit 1
fi
log "✓ Go $GO_VER"

if ! $SKIP_SERVICE && ! command -v systemctl &>/dev/null; then
  warn "systemd not found — service setup will be skipped (using --no-service mode)"
  SKIP_SERVICE=true
fi

# ── Build binary ──────────────────────────────────────────────────────────────
step "Building Binary"

VERSION=$(git describe --tags --always 2>/dev/null || echo "1.0.0")
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

log "Version: $VERSION"

# Ensure output directory exists
mkdir -p bin

if $IS_REBUILD; then
  log "Clean rebuild — removing cached build artifacts..."
  rm -f "bin/${APP_NAME}"
  GOTOOLCHAIN=local go clean -cache 2>/dev/null || true
fi

log "Running: GOTOOLCHAIN=local go build ..."
GOTOOLCHAIN=local go build \
  -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
  -o "bin/${APP_NAME}" . \
  || { error "Build failed — check Go source for errors"; exit 1; }

log "✓ Binary built: bin/${APP_NAME} ($(du -sh "bin/${APP_NAME}" | cut -f1))"

# ── Create system user ────────────────────────────────────────────────────────
step "Creating System User"

if ! $SKIP_SERVICE; then
  if [[ $EUID -ne 0 ]]; then
    warn "Not running as root — skipping system user creation."
    warn "Re-run with sudo for a full production install."
    SKIP_SERVICE=true
  else
    if ! id "$APP_USER" &>/dev/null; then
      useradd --system --no-create-home --shell /usr/sbin/nologin \
              --comment "GYDS Chain Fullnode" "$APP_USER"
      log "✓ Created system user: $APP_USER"
    else
      log "✓ System user already exists: $APP_USER"
    fi
  fi
fi

# ── Storage setup ─────────────────────────────────────────────────────────────
step "Configuring Storage"

mkdir -p "$GYDS_DATA_DIR"
log "✓ Data directory: $GYDS_DATA_DIR"

# Check available disk space
AVAIL_GB=$(df -BG "$GYDS_DATA_DIR" 2>/dev/null | awk 'NR==2{print $4}' | tr -d 'G' || echo "0")
log "Available disk space: ${AVAIL_GB} GB"

if [[ "$AVAIL_GB" -lt "$GYDS_STORAGE_LIMIT_GB" ]]; then
  warn "Available space (${AVAIL_GB} GB) is less than configured limit (${GYDS_STORAGE_LIMIT_GB} GB)"
  warn "The node will continue but may run out of storage"
fi

# Write disk-limit monitoring script
mkdir -p /etc/gyds-fullnode 2>/dev/null || mkdir -p "${INSTALL_DIR}/scripts"
SCRIPTS_DIR="/etc/gyds-fullnode"
[[ ! -d "$SCRIPTS_DIR" ]] && SCRIPTS_DIR="${SCRIPT_DIR}"

cat > "${SCRIPTS_DIR}/check-storage.sh" <<STORAGESCRIPT
#!/usr/bin/env bash
DATA_DIR="${GYDS_DATA_DIR}"
LIMIT_GB=${GYDS_STORAGE_LIMIT_GB}
USED_GB=\$(du -sBG "\$DATA_DIR" 2>/dev/null | cut -f1 | tr -d 'G' || echo "0")
AVAIL_GB=\$(df -BG "\$DATA_DIR" 2>/dev/null | awk 'NR==2{print \$4}' | tr -d 'G' || echo "0")
echo "Storage report — \$(date)"
echo "  Data dir  : \$DATA_DIR"
echo "  Used      : \${USED_GB} GB"
echo "  Available : \${AVAIL_GB} GB"
echo "  Limit     : \${LIMIT_GB} GB"
if [[ "\$AVAIL_GB" -lt 5 ]]; then
  echo "  WARNING: Less than 5 GB available!"
fi
STORAGESCRIPT
chmod +x "${SCRIPTS_DIR}/check-storage.sh"
log "✓ Storage monitor: ${SCRIPTS_DIR}/check-storage.sh"

# ── Install binary ────────────────────────────────────────────────────────────
step "Installing Binary"

if [[ $EUID -eq 0 ]]; then
  cp "bin/${APP_NAME}" "$BINARY_PATH"
  chmod 755 "$BINARY_PATH"
  chown root:root "$BINARY_PATH"
  log "✓ Installed to $BINARY_PATH"

  mkdir -p "$INSTALL_DIR"
  # Preserve existing .env on plain re-runs; only overwrite on --update/--rebuild
  if [[ ! -f "${INSTALL_DIR}/.env" ]] || $IS_UPDATE; then
    cp "$ENV_FILE" "${INSTALL_DIR}/.env"
    chmod 640 "${INSTALL_DIR}/.env"
    chown root:"$APP_USER" "${INSTALL_DIR}/.env" 2>/dev/null || true
    log "✓ Config installed to ${INSTALL_DIR}/.env"
  else
    log "✓ Existing config preserved at ${INSTALL_DIR}/.env (use --update to overwrite)"
  fi

  mkdir -p "$LOG_DIR"
  chown "$APP_USER":"$APP_USER" "$LOG_DIR" 2>/dev/null || true
  chown "$APP_USER":"$APP_USER" "$GYDS_DATA_DIR" 2>/dev/null || true
else
  warn "Not root — binary stays in ./bin/${APP_NAME}"
  BINARY_PATH="${SCRIPT_DIR}/bin/${APP_NAME}"
fi

# ── Firewall ──────────────────────────────────────────────────────────────────
step "Configuring Firewall"

if $SKIP_FIREWALL || [[ "${GYDS_ENABLE_FIREWALL:-true}" != "true" ]]; then
  warn "Firewall configuration skipped."
elif [[ $EUID -ne 0 ]]; then
  warn "Firewall configuration requires root. Run: sudo bash setup-firewall.sh"
elif command -v ufw &>/dev/null; then
  bash "${SCRIPT_DIR}/setup-firewall.sh" \
    --ssh-port "${GYDS_SSH_PORT:-22}" \
    --rpc-port "$GYDS_RPC_PORT" \
    --ws-port  "$GYDS_WS_PORT" \
    --p2p-port "$GYDS_P2P_PORT"
  log "✓ Firewall rules applied"
else
  warn "ufw not found — install with: sudo apt install ufw"
fi

# ── Systemd service ───────────────────────────────────────────────────────────
step "Setting Up System Service"

if $SKIP_SERVICE; then
  warn "Skipping systemd service setup."
else
  ENV_VARS=""
  while IFS='=' read -r key val; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    ENV_VARS+="Environment=\"${key}=${val}\"\n"
  done < "${INSTALL_DIR}/.env"

  cat > "$SERVICE_FILE" <<SYSTEMD
[Unit]
Description=GYDS Chain Full Node
Documentation=https://github.com/gydschain/fullnode
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=500
StartLimitBurst=5

[Service]
Type=simple
User=${APP_USER}
Group=${APP_GROUP}
WorkingDirectory=${INSTALL_DIR}
EnvironmentFile=${INSTALL_DIR}/.env
ExecStart=${BINARY_PATH} start
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=10s
TimeoutStopSec=60s

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${GYDS_DATA_DIR} ${LOG_DIR}

# Logging
StandardOutput=append:${LOG_DIR}/node.log
StandardError=append:${LOG_DIR}/error.log

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096

[Install]
WantedBy=multi-user.target
SYSTEMD

  chmod 644 "$SERVICE_FILE"
  systemctl daemon-reload

  # Smart start: restart if already running, start if stopped, enable+start if new
  if systemctl is-active --quiet gyds-fullnode 2>/dev/null; then
    systemctl restart gyds-fullnode
    log "✓ Service restarted"
  elif systemctl is-enabled --quiet gyds-fullnode 2>/dev/null; then
    systemctl start gyds-fullnode
    log "✓ Service started"
  else
    systemctl enable gyds-fullnode
    systemctl start  gyds-fullnode
    log "✓ Service enabled and started"
  fi

  sleep 3
  if systemctl is-active --quiet gyds-fullnode; then
    log "✓ Service is running"
  else
    warn "Service may not have started cleanly. Check: journalctl -u gyds-fullnode -n 50"
  fi
fi

# ── No-service mode: start directly ──────────────────────────────────────────
if $SKIP_SERVICE; then
  step "Starting Node (foreground)"
  log "Starting ${APP_NAME} directly..."
  log "Press Ctrl+C to stop."
  echo ""
  exec "${BINARY_PATH}" start
fi

# ── Health check ──────────────────────────────────────────────────────────────
step "Health Check"
sleep 5

MAX_TRIES=10
for i in $(seq 1 $MAX_TRIES); do
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    "http://localhost:${GYDS_RPC_PORT}/health" 2>/dev/null || echo "000")
  if [[ "$HTTP_CODE" == "200" ]]; then
    HEIGHT=$(curl -sf "http://localhost:${GYDS_RPC_PORT}/health" \
      | grep -o '"height":[0-9]*' | cut -d: -f2 || echo "?")
    log "✓ Node healthy — block height: ${HEIGHT}"
    break
  fi
  if [[ $i -eq $MAX_TRIES ]]; then
    warn "Node health check failed after ${MAX_TRIES} tries"
    warn "Check logs: journalctl -u gyds-fullnode -n 50"
  else
    info "Waiting for node to start... ($i/$MAX_TRIES)"
    sleep 3
  fi
done

# ── Completion summary ────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════════════╗"
echo -e "║        ✓  GYDS Chain Full Node  —  Deployed Successfully      ║"
echo -e "╚══════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  ${BOLD}Endpoints${NC}"
echo -e "  JSON-RPC  :  http://$(hostname -I | awk '{print $1}'):${GYDS_RPC_PORT}/"
echo -e "  WebSocket :  ws://$(hostname -I | awk '{print $1}'):${GYDS_WS_PORT}/"
echo -e "  P2P       :  $(hostname -I | awk '{print $1}'):${GYDS_P2P_PORT}"
echo -e "  Dashboard :  http://$(hostname -I | awk '{print $1}'):${GYDS_RPC_PORT}/"
echo -e "  Setup     :  http://$(hostname -I | awk '{print $1}'):${GYDS_RPC_PORT}/setup"
echo ""
echo -e "  ${BOLD}Useful commands${NC}"
echo -e "  Status   :  sudo systemctl status gyds-fullnode"
echo -e "  Logs     :  sudo journalctl -u gyds-fullnode -f"
echo -e "  Stop     :  sudo systemctl stop gyds-fullnode"
echo -e "  Restart  :  sudo systemctl restart gyds-fullnode"
echo -e "  Update   :  bash deploy.sh --update"
echo -e "  Storage  :  bash ${SCRIPTS_DIR}/check-storage.sh"
echo ""
