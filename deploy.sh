#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
#  GYDS Chain — Production Deployment Script
#
#  Usage:
#    bash deploy.sh [OPTIONS]
#
#  Options:
#    --env FILE        Path to .env file (default: .env)
#    --dashboard-port PORT  Dashboard web UI port (default: 5000)
#    --no-firewall     Skip firewall configuration
#    --no-service      Run in the foreground instead of installing a systemd service
#    --update          Rebuild the binary and restart the service (preserves chain data)
#    --rebuild         Full clean rebuild: remove cached build artifacts before building
#    --uninstall       Remove the GYDS fullnode service, binary, and installation files
#    --status          Show the current service status and chain health
#    --help            Show this help message
#
#  Prerequisites:
#    - Go 1.21 or later (installed automatically if missing or outdated)
#    - systemd (required for service management)
#    - ufw (required for firewall hardening)
#    - A configured .env file (copy .env.example to .env and edit it),
#      or first run the web setup wizard at http://<host>:<dashboard_port>/setup
#
#  Safe to run multiple times — already-completed steps are skipped.
# ══════════════════════════════════════════════════════════════════════════════
set -euo pipefail
IFS=$'\n\t'

# ── Colors ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; YELLOW='\033[1;33m'; GREEN='\033[0;32m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

# ── Logging ───────────────────────────────────────────────────────────────────
log()   { echo -e "${GREEN}[$(date '+%H:%M:%S')] ✓${NC} $*"; }
info()  { echo -e "${CYAN}[$(date '+%H:%M:%S')]  ${NC} $*"; }
warn()  { echo -e "${YELLOW}[$(date '+%H:%M:%S')] ⚠ WARNING:${NC} $*" >&2; }
error() { echo -e "${RED}[$(date '+%H:%M:%S')] ✗ ERROR:${NC} $*" >&2; }
step()  { echo -e "\n${BOLD}${CYAN}━━━  $*  ━━━${NC}\n"; }
die()   { error "$*"; exit 1; }

banner() {
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

# ── Defaults ──────────────────────────────────────────────────────────────────
ENV_FILE=".env"
SKIP_FIREWALL=false
SKIP_SERVICE=false
IS_UPDATE=false
IS_REBUILD=false
UNINSTALL=false
SHOW_STATUS=false
DASHBOARD_PORT_OVERRIDE=""

APP_NAME="gyds-fullnode"
APP_USER="gyds"
APP_GROUP="gyds"
INSTALL_DIR="/opt/gyds-fullnode"
BINARY_PATH="/usr/local/bin/gyds-fullnode"
SERVICE_FILE="/etc/systemd/system/gyds-fullnode.service"
LOG_DIR="/var/log/gyds-fullnode"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --env)          ENV_FILE="$2"; shift 2 ;;
    --dashboard-port) DASHBOARD_PORT_OVERRIDE="$2"; shift 2 ;;
    --no-firewall)  SKIP_FIREWALL=true; shift ;;
    --no-service)   SKIP_SERVICE=true; shift ;;
    --update)       IS_UPDATE=true; shift ;;
    --rebuild)      IS_REBUILD=true; IS_UPDATE=true; shift ;;
    --uninstall)    UNINSTALL=true; shift ;;
    --status)       SHOW_STATUS=true; shift ;;
    --help|-h)
      grep '^#  ' "$0" | head -20 | sed 's/^#  \?//'
      exit 0 ;;
    *)
      die "Unknown option: '$1'  Run 'bash deploy.sh --help' for usage." ;;
  esac
done

banner

# ── Status ────────────────────────────────────────────────────────────────────
if $SHOW_STATUS; then
  step "Service Status"
  if systemctl is-active --quiet gyds-fullnode 2>/dev/null; then
    systemctl status gyds-fullnode --no-pager
  else
    warn "Service 'gyds-fullnode' is not running."
  fi
  echo ""
  _rpc_port=$(grep "^GYDS_RPC_PORT" "${ENV_FILE}" 2>/dev/null | cut -d= -f2 | tr -d ' ' || true)
  _rpc_port="${_rpc_port:-8545}"
  info "Checking node health on port ${_rpc_port}..."
  if curl -sf "http://localhost:${_rpc_port}/health" | python3 -m json.tool 2>/dev/null; then
    :
  else
    warn "Node is not responding on port ${_rpc_port}."
  fi
  exit 0
fi

# ── Uninstall ─────────────────────────────────────────────────────────────────
if $UNINSTALL; then
  step "Uninstalling GYDS Fullnode"
  [[ $EUID -ne 0 ]] && die "Uninstall requires root. Run: sudo bash deploy.sh --uninstall"

  echo -e "${RED}This will stop the service and remove all installation files.${NC}"
  echo -e "${YELLOW}Chain data at /var/lib/gyds-fullnode will be preserved.${NC}"
  read -rp "Continue? [y/N]: " _confirm
  [[ "${_confirm,,}" != "y" ]] && { info "Aborted."; exit 0; }

  systemctl stop    gyds-fullnode 2>/dev/null || true
  systemctl disable gyds-fullnode 2>/dev/null || true
  rm -f  "$SERVICE_FILE"
  rm -f  "$BINARY_PATH"
  rm -rf "$INSTALL_DIR"
  rm -rf "$LOG_DIR"
  systemctl daemon-reload 2>/dev/null || true
  log "Uninstall complete."
  info "Chain data preserved at:  /var/lib/gyds-fullnode"
  info "To remove chain data:     sudo rm -rf /var/lib/gyds-fullnode"
  exit 0
fi

# ── Load .env ─────────────────────────────────────────────────────────────────
step "Loading Configuration"

if [[ ! -f "$ENV_FILE" ]]; then
  error ".env file not found: ${ENV_FILE}"
  echo ""
  echo "  Option 1 — copy the example file and fill in your values:"
  echo "    cp .env.example .env && nano .env"
  echo ""
  echo "  Option 2 — use the web setup wizard:"
  echo "    go run . start"
  echo "    Then open: http://localhost:5000/setup"
  echo ""
  exit 1
fi

[[ -s "$ENV_FILE" ]] || die ".env file is empty: ${ENV_FILE}"

set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

# Apply defaults for any variable not present in .env
GYDS_CHAIN_ID="${GYDS_CHAIN_ID:-198282}"
GYDS_NODE_MODE="${GYDS_NODE_MODE:-full}"
GYDS_DASHBOARD_PORT="${GYDS_DASHBOARD_PORT:-5000}"
GYDS_RPC_PORT="${GYDS_RPC_PORT:-8545}"
GYDS_WS_PORT="${GYDS_WS_PORT:-8546}"
GYDS_P2P_PORT="${GYDS_P2P_PORT:-30303}"
GYDS_RPC_HOST="${GYDS_RPC_HOST:-0.0.0.0}"
GYDS_DATA_DIR="${GYDS_DATA_DIR:-/var/lib/gyds-fullnode}"
GYDS_LOG_LEVEL="${GYDS_LOG_LEVEL:-info}"
GYDS_LOG_FORMAT="${GYDS_LOG_FORMAT:-json}"
GYDS_SSH_PORT="${GYDS_SSH_PORT:-22}"
GYDS_STORAGE_LIMIT_GB="${GYDS_STORAGE_LIMIT_GB:-50}"

if [[ -n "$DASHBOARD_PORT_OVERRIDE" ]]; then
  GYDS_DASHBOARD_PORT="$DASHBOARD_PORT_OVERRIDE"
fi

# systemd requires every path in ReadWritePaths= to be absolute. Resolve a
# relative path against the checkout before creating the service, and persist
# the resolved value in the installed environment so the service and binary
# use the same directory after reboot.
if [[ "$GYDS_DATA_DIR" != /* ]]; then
  _original_data_dir="$GYDS_DATA_DIR"
  if command -v realpath >/dev/null 2>&1; then
    GYDS_DATA_DIR="$(realpath -m -- "${SCRIPT_DIR}/${GYDS_DATA_DIR}")"
  else
    GYDS_DATA_DIR="${SCRIPT_DIR%/}/${GYDS_DATA_DIR#./}"
  fi
  info "Resolved relative data directory '${_original_data_dir}' to '${GYDS_DATA_DIR}'"
fi

[[ "$GYDS_DATA_DIR" != *$'\n'* && "$GYDS_DATA_DIR" != *$'\r'* ]] \
  || die "GYDS_DATA_DIR contains a newline, which is not valid for a systemd service."

# Validate port numbers
validate_port() {
  local name="$1" value="$2"
  if ! [[ "$value" =~ ^[0-9]+$ ]] || [[ "$value" -lt 1 || "$value" -gt 65535 ]]; then
    die "${name}=${value} is not a valid port number (must be 1–65535)."
  fi
}
validate_port "GYDS_DASHBOARD_PORT" "$GYDS_DASHBOARD_PORT"
validate_port "GYDS_RPC_PORT"       "$GYDS_RPC_PORT"
validate_port "GYDS_WS_PORT"        "$GYDS_WS_PORT"
validate_port "GYDS_P2P_PORT"       "$GYDS_P2P_PORT"
validate_port "GYDS_SSH_PORT"       "$GYDS_SSH_PORT"

# Detect port conflicts
declare -A _seen_ports=()
for _pname in GYDS_DASHBOARD_PORT GYDS_RPC_PORT GYDS_WS_PORT GYDS_P2P_PORT; do
  _pval="${!_pname}"
  if [[ -n "${_seen_ports[$_pval]:-}" ]]; then
    die "Port conflict: ${_pname} and ${_seen_ports[$_pval]} are both set to port ${_pval}."
  fi
  _seen_ports["$_pval"]="$_pname"
done

# Validate node mode
case "$GYDS_NODE_MODE" in
  full|lite|rpc|boost|genesis|sync|validator|testnode) ;;
  *) die "GYDS_NODE_MODE='${GYDS_NODE_MODE}' is not a recognised node mode." ;;
esac

log "Chain ID       : $GYDS_CHAIN_ID"
log "Node mode      : $GYDS_NODE_MODE"
log "Dashboard port : $GYDS_DASHBOARD_PORT"
log "RPC port       : $GYDS_RPC_PORT"
log "WebSocket port : $GYDS_WS_PORT"
log "P2P port       : $GYDS_P2P_PORT"
log "Data directory : $GYDS_DATA_DIR"
log "Storage limit  : ${GYDS_STORAGE_LIMIT_GB} GB"

# ── Prerequisites ─────────────────────────────────────────────────────────────
step "Checking Prerequisites"

# Install a system package when the corresponding command is not available.
require_cmd() {
  local cmd="$1" pkg="${2:-$1}"
  if command -v "$cmd" &>/dev/null; then
    log "${cmd}"
    return 0
  fi
  info "${cmd} not found — installing ${pkg}..."
  if [[ $EUID -ne 0 ]]; then
    die "'${cmd}' is required but is not installed. Install it with: sudo apt install ${pkg}"
  fi
  if command -v apt-get &>/dev/null; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y "$pkg" >/dev/null 2>&1 \
      || die "Failed to install '${pkg}' via apt-get."
  elif command -v dnf &>/dev/null; then
    dnf install -y "$pkg" >/dev/null 2>&1 \
      || die "Failed to install '${pkg}' via dnf."
  else
    die "'${cmd}' is required but could not be installed automatically. Install '${pkg}' manually."
  fi
  log "${cmd} (installed)"
}

require_cmd curl
require_cmd git
require_cmd jq
require_cmd tar
require_cmd awk
require_cmd sed
require_cmd grep
require_cmd hostname

# python3 is optional — used only for JSON pretty-printing in --status mode.
if command -v python3 &>/dev/null; then
  log "python3"
else
  info "python3 not found — JSON output in --status mode will not be pretty-printed."
fi

# ── Go installation ───────────────────────────────────────────────────────────
GO_MIN_MAJOR=1
GO_MIN_MINOR=21

# Fetch the latest stable Go version string from go.dev.
fetch_latest_go_version() {
  curl -fsSL --max-time 15 "https://go.dev/VERSION?m=text" 2>/dev/null \
    | head -1 | sed 's/^go//' \
    || echo "1.22.5"
}

# Download, verify, and install Go at the given version.
install_go() {
  local target_ver="$1"
  local arch
  arch=$(uname -m)
  case "$arch" in
    x86_64)  arch="amd64" ;;
    aarch64) arch="arm64" ;;
    armv6l)  arch="armv6l" ;;
    *) die "Unsupported CPU architecture: ${arch}" ;;
  esac

  local tarball="go${target_ver}.linux-${arch}.tar.gz"
  local url="https://go.dev/dl/${tarball}"
  local tmp_tar="/tmp/${tarball}"
  local tmp_sha="/tmp/${tarball}.sha256"

  info "Downloading Go ${target_ver} (linux/${arch})..."
  curl -fsSL --max-time 180 --retry 3 "$url" -o "$tmp_tar" \
    || die "Failed to download Go from: ${url}"

  # Verify SHA256 checksum when available.
  info "Verifying download integrity..."
  if curl -fsSL --max-time 15 "${url}.sha256" -o "$tmp_sha" 2>/dev/null && [[ -s "$tmp_sha" ]]; then
    local expected actual
    expected=$(cat "$tmp_sha")
    actual=$(sha256sum "$tmp_tar" | awk '{print $1}')
    if [[ "$expected" != "$actual" ]]; then
      rm -f "$tmp_tar" "$tmp_sha"
      die "SHA256 checksum mismatch for ${tarball}. The download may be corrupted — please retry."
    fi
    log "Checksum verified"
  else
    warn "Could not retrieve checksum file — skipping integrity verification."
  fi

  # Extract to a temporary location and test the binary before replacing the live install.
  info "Extracting Go ${target_ver}..."
  local tmp_extract="/tmp/go-extract-$$"
  mkdir -p "$tmp_extract"
  if ! tar -C "$tmp_extract" -xzf "$tmp_tar"; then
    rm -rf "$tmp_extract" "$tmp_tar" "$tmp_sha"
    die "Failed to extract Go archive."
  fi

  # Verify the extracted binary works before removing the existing install.
  if ! "$tmp_extract/go/bin/go" version &>/dev/null; then
    rm -rf "$tmp_extract" "$tmp_tar" "$tmp_sha"
    die "Extracted Go binary failed self-test. The archive may be incomplete."
  fi

  rm -rf /usr/local/go
  mv "$tmp_extract/go" /usr/local/go
  rm -rf "$tmp_extract" "$tmp_tar" "$tmp_sha"

  export PATH="/usr/local/go/bin:$PATH"

  if [[ ! -f /etc/profile.d/go.sh ]]; then
    echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
    chmod 644 /etc/profile.d/go.sh
  fi

  log "Go ${target_ver} installed to /usr/local/go"
}

_need_go_install=false
if ! command -v go &>/dev/null; then
  if [[ $EUID -ne 0 ]]; then
    die "Go is not installed. Run as root for automatic installation, or install manually from https://go.dev/dl/"
  fi
  _need_go_install=true
else
  _go_ver=$(go version | awk '{print $3}' | sed 's/^go//')
  _go_major=$(echo "$_go_ver" | cut -d. -f1)
  _go_minor=$(echo "$_go_ver" | cut -d. -f2)
  if [[ "$_go_major" -lt "$GO_MIN_MAJOR" || \
       ( "$_go_major" -eq "$GO_MIN_MAJOR" && "$_go_minor" -lt "$GO_MIN_MINOR" ) ]]; then
    warn "Go ${_go_ver} is below the minimum required version (${GO_MIN_MAJOR}.${GO_MIN_MINOR}) — upgrading."
    if [[ $EUID -ne 0 ]]; then
      die "Root is required to upgrade Go. Run: sudo bash deploy.sh"
    fi
    _need_go_install=true
  else
    log "Go ${_go_ver}"
  fi
fi

if $_need_go_install; then
  _latest_go=$(fetch_latest_go_version)
  install_go "$_latest_go"
  _go_ver=$(go version | awk '{print $3}' | sed 's/^go//')
  log "Go ${_go_ver} ready"
fi

if ! $SKIP_SERVICE && ! command -v systemctl &>/dev/null; then
  warn "systemd is not available — service setup will be skipped."
  SKIP_SERVICE=true
fi

# ── Build binary ──────────────────────────────────────────────────────────────
step "Building Binary"

[[ -f "go.mod" ]] || die "go.mod not found. Is this the GYDS source directory?"
[[ -f "go.sum" ]] || die "go.sum not found. Run 'go mod tidy' to generate it."

_version=$(git describe --tags --always 2>/dev/null || echo "dev")
_build_time=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
_go_version=$(go version | awk '{print $3}')

info "Version    : ${_version}"
info "Go version : ${_go_version}"
info "Build time : ${_build_time}"

info "Downloading module dependencies..."
GOTOOLCHAIN=local go mod download \
  || die "Failed to download Go module dependencies. Check your network connection."

mkdir -p bin

if $IS_REBUILD; then
  info "Clean rebuild — removing cached build artifacts..."
  rm -f "bin/${APP_NAME}"
  GOTOOLCHAIN=local go clean -cache 2>/dev/null || true
fi

info "Compiling..."
if ! GOTOOLCHAIN=local go build \
      -ldflags="-s -w -X main.version=${_version} -X main.buildTime=${_build_time}" \
      -o "bin/${APP_NAME}" .; then
  error "Build failed. Run 'go build .' to see the full error output."
  exit 1
fi

_binary_size=$(du -sh "bin/${APP_NAME}" | cut -f1)
log "Binary built: bin/${APP_NAME}  (${_binary_size})"

# ── System user ───────────────────────────────────────────────────────────────
step "Creating System User"

if ! $SKIP_SERVICE; then
  if [[ $EUID -ne 0 ]]; then
    warn "Not running as root — skipping system user creation."
    SKIP_SERVICE=true
  else
    if ! id "$APP_USER" &>/dev/null; then
      useradd --system --no-create-home --shell /usr/sbin/nologin \
              --comment "GYDS Chain Fullnode" "$APP_USER"
      log "Created system user: ${APP_USER}"
    else
      log "System user already exists: ${APP_USER}"
    fi
  fi
fi

# ── Storage ───────────────────────────────────────────────────────────────────
step "Configuring Storage"

if ! mkdir -p "$GYDS_DATA_DIR" 2>/dev/null; then
  if [[ $EUID -ne 0 ]]; then
    warn "Cannot create ${GYDS_DATA_DIR} (requires root). Falling back to ./data"
    GYDS_DATA_DIR="${SCRIPT_DIR}/data"
    mkdir -p "$GYDS_DATA_DIR" \
      || die "Failed to create fallback data directory: ${GYDS_DATA_DIR}"
  else
    die "Failed to create data directory: ${GYDS_DATA_DIR}"
  fi
fi
log "Data directory: ${GYDS_DATA_DIR}"

# ReadWritePaths= accepts absolute, whitespace-free paths only. Validate the
# final value after the non-root fallback as well as the initial environment
# normalization above, so a malformed unit cannot be installed.
validate_systemd_path() {
  local name="$1" value="$2"
  [[ "$value" == /* ]] || die "${name}=${value} must be an absolute path for systemd."
  [[ "$value" != *[[:space:]]* ]] ||
    die "${name} contains whitespace and cannot be used in ReadWritePaths=: ${value}"
}
validate_systemd_path "GYDS_DATA_DIR" "$GYDS_DATA_DIR"
validate_systemd_path "LOG_DIR" "$LOG_DIR"

_avail_gb=$(df -BG "$GYDS_DATA_DIR" 2>/dev/null | awk 'NR==2{print $4}' | tr -d 'G' || echo 0)
_avail_gb="${_avail_gb:-0}"
info "Available disk space: ${_avail_gb} GB  (configured limit: ${GYDS_STORAGE_LIMIT_GB} GB)"

if [[ "$_avail_gb" -lt "$GYDS_STORAGE_LIMIT_GB" ]]; then
  warn "Available disk space (${_avail_gb} GB) is less than the configured limit (${GYDS_STORAGE_LIMIT_GB} GB)."
  warn "The node may run out of storage. Consider freeing space or reducing GYDS_STORAGE_LIMIT_GB."
fi

# Write a storage monitoring script
if [[ $EUID -eq 0 ]]; then
  mkdir -p /etc/gyds-fullnode
  _scripts_dir="/etc/gyds-fullnode"
else
  mkdir -p "${SCRIPT_DIR}/scripts"
  _scripts_dir="${SCRIPT_DIR}/scripts"
fi

cat > "${_scripts_dir}/check-storage.sh" <<STORAGESCRIPT
#!/usr/bin/env bash
DATA_DIR="${GYDS_DATA_DIR}"
LIMIT_GB=${GYDS_STORAGE_LIMIT_GB}
USED_GB=\$(du -sBG "\$DATA_DIR" 2>/dev/null | cut -f1 | tr -d 'G' || echo 0)
AVAIL_GB=\$(df -BG "\$DATA_DIR" 2>/dev/null | awk 'NR==2{print \$4}' | tr -d 'G' || echo 0)
echo "Storage report — \$(date)"
echo "  Data directory : \$DATA_DIR"
echo "  Used           : \${USED_GB} GB"
echo "  Available      : \${AVAIL_GB} GB"
echo "  Limit          : \${LIMIT_GB} GB"
if [[ "\${AVAIL_GB}" -lt 5 ]]; then
  echo "  WARNING: Less than 5 GB remaining — node may stop soon!"
fi
STORAGESCRIPT
chmod +x "${_scripts_dir}/check-storage.sh"
log "Storage monitor: ${_scripts_dir}/check-storage.sh"

# ── Install binary ────────────────────────────────────────────────────────────
step "Installing Binary"

if [[ $EUID -eq 0 ]]; then
  cp "bin/${APP_NAME}" "$BINARY_PATH"
  chmod 755 "$BINARY_PATH"
  chown root:root "$BINARY_PATH"
  log "Binary installed to: ${BINARY_PATH}"

  mkdir -p "$INSTALL_DIR"
  if [[ ! -f "${INSTALL_DIR}/.env" ]] || $IS_UPDATE; then
    cp "$ENV_FILE" "${INSTALL_DIR}/.env"
    chmod 640 "${INSTALL_DIR}/.env"
    chown root:"$APP_USER" "${INSTALL_DIR}/.env" 2>/dev/null || true
    log "Configuration installed to: ${INSTALL_DIR}/.env"
  else
    log "Existing configuration preserved at: ${INSTALL_DIR}/.env  (use --update to overwrite)"
  fi

  # Keep the service-owned configuration aligned with the absolute path used
  # in the generated unit, even when an existing .env is intentionally kept.
  if grep -q '^GYDS_DATA_DIR=' "${INSTALL_DIR}/.env"; then
    sed -i "s|^GYDS_DATA_DIR=.*|GYDS_DATA_DIR=${GYDS_DATA_DIR}|" "${INSTALL_DIR}/.env"
  else
    printf 'GYDS_DATA_DIR=%s\n' "$GYDS_DATA_DIR" >> "${INSTALL_DIR}/.env"
  fi

  # Node mode is a deployment choice, not chain state or a secret. Keep the
  # installed service aligned with the mode selected in the .env passed to this
  # deployment, even when the rest of an existing configuration is preserved.
  if grep -q '^GYDS_NODE_MODE=' "${INSTALL_DIR}/.env"; then
    sed -i "s|^GYDS_NODE_MODE=.*|GYDS_NODE_MODE=${GYDS_NODE_MODE}|" "${INSTALL_DIR}/.env"
  else
    printf 'GYDS_NODE_MODE=%s\n' "$GYDS_NODE_MODE" >> "${INSTALL_DIR}/.env"
  fi

  mkdir -p "$LOG_DIR"
  chown "${APP_USER}:${APP_USER}" "$LOG_DIR"       2>/dev/null || true
  chown "${APP_USER}:${APP_USER}" "$GYDS_DATA_DIR" 2>/dev/null || true

  if [[ -f "${SCRIPT_DIR}/scripts/update-from-git.sh" ]]; then
    cat > /usr/local/bin/gyds-fullnode-update <<UPDATE_WRAPPER
#!/usr/bin/env bash
export GYDS_APP_DIR="${SCRIPT_DIR}"
exec "${SCRIPT_DIR}/scripts/update-from-git.sh" "\$@"
UPDATE_WRAPPER
    chmod 755 /usr/local/bin/gyds-fullnode-update
    log "Git update helper installed: /usr/local/bin/gyds-fullnode-update"
  fi
else
  warn "Not running as root — binary remains in: ./bin/${APP_NAME}"
  BINARY_PATH="${SCRIPT_DIR}/bin/${APP_NAME}"
fi

# ── Firewall ──────────────────────────────────────────────────────────────────
step "Configuring Firewall"

if $SKIP_FIREWALL || [[ "${GYDS_ENABLE_FIREWALL}" != "true" ]]; then
  warn "Firewall configuration skipped."
elif [[ $EUID -ne 0 ]]; then
  warn "Firewall configuration requires root. When ready, run:"
  warn "  sudo bash setup-firewall.sh \\"
  warn "    --ssh-port ${GYDS_SSH_PORT} \\"
  warn "    --dashboard-port ${GYDS_DASHBOARD_PORT} \\"
  warn "    --rpc-port ${GYDS_RPC_PORT} \\"
  warn "    --ws-port ${GYDS_WS_PORT} \\"
  warn "    --p2p-port ${GYDS_P2P_PORT}"
elif ! command -v ufw &>/dev/null; then
  warn "ufw is not installed. Install it with: sudo apt install ufw"
  warn "Then run: sudo bash setup-firewall.sh"
else
  # Keep deployment limited to the UFW network boundary.
  bash "${SCRIPT_DIR}/setup-firewall.sh" \
    --ssh-port       "${GYDS_SSH_PORT}" \
    --dashboard-port "${GYDS_DASHBOARD_PORT}" \
    --rpc-port       "${GYDS_RPC_PORT}" \
    --ws-port        "${GYDS_WS_PORT}" \
    --p2p-port       "${GYDS_P2P_PORT}" \
    --data-dir       "${GYDS_DATA_DIR}" \
    --ufw-only \
    || die "setup-firewall.sh failed — see output above for details."
  log "Firewall rules applied"
fi

# ── Systemd service ───────────────────────────────────────────────────────────
step "Setting Up System Service"

if $SKIP_SERVICE; then
  warn "Skipping systemd service setup."
else
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
# Explicit values keep command-line port overrides effective even when an
# existing installed .env is intentionally preserved.
Environment=GYDS_DASHBOARD_PORT=${GYDS_DASHBOARD_PORT}
Environment=GYDS_RPC_PORT=${GYDS_RPC_PORT}
Environment=GYDS_WS_PORT=${GYDS_WS_PORT}
Environment=GYDS_P2P_PORT=${GYDS_P2P_PORT}
Environment=GYDS_DATA_DIR=${GYDS_DATA_DIR}
Environment=GYDS_NODE_MODE=${GYDS_NODE_MODE}
ExecStart=${BINARY_PATH} start
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=10s
TimeoutStopSec=60s

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
LockPersonality=true
RestrictNamespaces=true
RestrictRealtime=true
SystemCallArchitectures=native
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
  _rw_paths="$(awk -F= '/^ReadWritePaths=/{print $2}' "$SERVICE_FILE")"
  for _rw_path in $_rw_paths; do
    [[ "$_rw_path" == /* ]] ||
      die "Generated systemd service contains a non-absolute ReadWritePaths entry: ${_rw_path}"
  done
  if command -v systemd-analyze >/dev/null 2>&1; then
    systemd-analyze verify "$SERVICE_FILE" \
      || die "Generated systemd service is invalid. Review ${SERVICE_FILE}."
  fi

  if systemctl is-active --quiet gyds-fullnode 2>/dev/null; then
    systemctl restart gyds-fullnode && log "Service restarted"
  elif systemctl is-enabled --quiet gyds-fullnode 2>/dev/null; then
    systemctl start gyds-fullnode && log "Service started"
  else
    systemctl enable gyds-fullnode
    systemctl start  gyds-fullnode && log "Service enabled and started"
  fi

  sleep 3
  if systemctl is-active --quiet gyds-fullnode; then
    log "Service is running"
  else
    error "Service failed to start. Diagnostics:"
    journalctl -u gyds-fullnode -n 30 --no-pager >&2 || true
    die "Resolve the errors above and retry. Run 'bash deploy.sh --update' after fixing."
  fi
fi

# ── Foreground mode ───────────────────────────────────────────────────────────
if $SKIP_SERVICE; then
  step "Starting Node (foreground)"
  info "Starting ${APP_NAME} directly. Press Ctrl+C to stop."
  echo ""
  exec "${BINARY_PATH}" start
fi

# ── Health check ──────────────────────────────────────────────────────────────
step "Health Check"
info "Waiting for the node to initialize..."
sleep 5

_max_tries=12
_rpc_healthy=false

for _i in $(seq 1 "$_max_tries"); do
  _http=$(curl -s -o /dev/null -w "%{http_code}" \
    "http://localhost:${GYDS_RPC_PORT}/health" 2>/dev/null || echo "000")
  if [[ "$_http" == "200" ]]; then
    _height=$(curl -sf "http://localhost:${GYDS_RPC_PORT}/health" 2>/dev/null \
      | grep -o '"height":[0-9]*' | cut -d: -f2 || echo "?")
    log "RPC health check passed — block height: ${_height}"
    _rpc_healthy=true
    break
  fi
  info "Waiting for node to respond... (${_i}/${_max_tries})"
  sleep 3
done

if ! $_rpc_healthy; then
  warn "RPC health check timed out. The node may still be starting."
  warn "Check logs: sudo journalctl -u gyds-fullnode -f"
fi

# Dashboard check
_dash_code=$(curl -s -o /dev/null -w "%{http_code}" \
  "http://localhost:${GYDS_DASHBOARD_PORT}/" 2>/dev/null || echo "000")
if [[ "$_dash_code" =~ ^(200|301|302)$ ]]; then
  log "Dashboard is reachable on port ${GYDS_DASHBOARD_PORT}"
else
  warn "Dashboard not responding on port ${GYDS_DASHBOARD_PORT} (HTTP ${_dash_code})."
fi

# WebSocket check (curl sends an upgrade request and expects HTTP 101)
_ws_code=$(curl -s -o /dev/null -w "%{http_code}" \
  --max-time 5 \
  -H "Upgrade: websocket" \
  -H "Connection: Upgrade" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" \
  "http://localhost:${GYDS_RPC_PORT}/ws" 2>/dev/null || echo "000")
if [[ "$_ws_code" == "101" ]]; then
  log "WebSocket endpoint reachable"
else
  info "WebSocket endpoint not yet ready (HTTP ${_ws_code}) — this is normal if the node is still syncing."
fi

# ── Completion summary ────────────────────────────────────────────────────────
_host_ip=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "127.0.0.1")

echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════════════╗"
echo -e "║       ✓  GYDS Chain Full Node — Deployed Successfully         ║"
echo -e "╚══════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  ${BOLD}Endpoints${NC}"
echo -e "  Dashboard :  http://${_host_ip}:${GYDS_DASHBOARD_PORT}/"
echo -e "  Setup     :  http://${_host_ip}:${GYDS_DASHBOARD_PORT}/setup"
echo -e "  JSON-RPC  :  http://${_host_ip}:${GYDS_RPC_PORT}/"
echo -e "  WebSocket :  ws://${_host_ip}:${GYDS_WS_PORT}/"
echo -e "  P2P       :  ${_host_ip}:${GYDS_P2P_PORT}"
echo ""
echo -e "  ${BOLD}Useful commands${NC}"
echo -e "  Status   :  sudo systemctl status gyds-fullnode"
echo -e "  Logs     :  sudo journalctl -u gyds-fullnode -f"
echo -e "  Stop     :  sudo systemctl stop gyds-fullnode"
echo -e "  Restart  :  sudo systemctl restart gyds-fullnode"
echo -e "  Update   :  bash deploy.sh --update"
echo -e "  Storage  :  bash ${_scripts_dir}/check-storage.sh"
echo ""
