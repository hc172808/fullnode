#!/usr/bin/env bash
# ============================================================
# GYDS Chain — Full Node Setup
# Supports: Ubuntu 20.04/22.04/24.04, Debian 11/12,
#           CentOS Stream 8/9, RHEL 8/9, AlmaLinux 8/9,
#           Rocky Linux 8/9, Fedora 38+
#
# Usage: sudo bash setup-fullnode-server.sh [OPTIONS]
#
# Options:
#   --dashboard-port PORT Dashboard web UI port (default: 5000)
#   --datadir  DIR     Chain data directory (default: /var/lib/gyds-fullnode)
#   --rpc-port PORT    RPC port (default: 8545)
#   --ws-port  PORT    WebSocket port (default: 8546)
#   --p2p-port PORT    P2P port (default: 30303)
#   --ssh-port PORT    SSH port for firewall (default: 22)
#   --no-fail2ban      Skip optional Fail2ban installation/configuration
#   --domain   DOMAIN  Domain name for TLS/HTTPS via Certbot (optional)
#   --log-level LEVEL  Log level: trace|debug|info|warn|error (default: info)
#   --no-docker        Skip Docker installation (run as native systemd service)
#   --update           Update an existing installation
#   --uninstall        Remove GYDS fullnode and all related files
#   --help             Show this help and port reference, then exit
# ============================================================
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
APP_NAME="gyds-fullnode"
APP_USER="gyds"
APP_DIR="/opt/gyds-fullnode"
REPO_URL="https://github.com/hc172808/fullnode.git"
BRANCH="main"
GO_VERSION="1.25.5"

GYDS_DATADIR="${GYDS_DATADIR:-/var/lib/gyds-fullnode}"
GYDS_CHAIN_ID="${GYDS_CHAIN_ID:-198282}"
GYDS_NODE_MODE="${GYDS_NODE_MODE:-full}"
GYDS_DASHBOARD_PORT="${GYDS_DASHBOARD_PORT:-5000}"
GYDS_RPC_PORT="${GYDS_RPC_PORT:-8545}"
GYDS_WS_PORT="${GYDS_WS_PORT:-8546}"
GYDS_P2P_PORT="${GYDS_P2P_PORT:-30303}"
SSH_PORT="22"
DOMAIN=""
LOG_LEVEL="info"
BOOTSTRAP_NODES=""
ALLOW_IPS=""
DISK_ALERT_GB=10
USE_DOCKER=true
USE_FAIL2BAN=true
IS_UPDATE=false
FORCE_REBUILD=false
UNINSTALL=false

# ── Argument parsing ───────────────────────────────────────────────────────────
show_help() {
  cat <<EOF

GYDS Chain — Full Node Setup Script
====================================

Usage:
  sudo bash setup-fullnode-server.sh [OPTIONS]

OPTIONS
  --dashboard-port PORT Dashboard web UI port      (default: 5000)
  --rpc-port  PORT   JSON-RPC HTTP port          (default: 8545)
  --ws-port   PORT   WebSocket port              (default: 8546)
  --p2p-port  PORT   P2P peer networking port    (default: 30303)
  --ssh-port  PORT   SSH port (firewall allow)   (default: 22)
  --no-fail2ban      Skip optional Fail2ban installation/configuration
  --datadir   DIR    Chain data directory        (default: /var/lib/gyds-fullnode)
  --domain    FQDN   Domain for auto-TLS (Certbot, requires DNS → this IP)
  --log-level LEVEL  trace | debug | info | warn | error  (default: info)
  --bootstrap-nodes  Comma-separated peer list  e.g. tcp://1.2.3.4:30303,tcp://5.6.7.8:30303
  --allow-ip  IP     Restrict RPC/WS to this IP only (can repeat for multiple IPs)
  --disk-alert GB    Warn when free disk drops below this GB threshold (default: 10)
  --rebuild          Wipe the app directory and do a full clean reinstall
                     (chain data in --datadir is always preserved)
  --no-docker        Run as native systemd service instead of Docker
  --update           Pull latest code and restart (keeps .env and data)
  --uninstall        Remove node, service, nginx config (preserves chain data)
  --help             Show this help and exit

IDEMPOTENT — safe to run multiple times:
  • No flags  : resumes from existing state; skips already-done steps
  • --update  : pulls latest code, backs up data, restarts service
  • --rebuild : wipes code dir only (data safe), clones fresh, rebuilds

PORT REFERENCE
  ┌─────────┬──────────┬─────────────────────────────────────────────────────┐
  │ Port    │ Protocol │ Purpose                                             │
  ├─────────┼──────────┼─────────────────────────────────────────────────────┤
  │  5000   │ TCP      │ Dashboard web UI (use --dashboard-port to change)   │
  │  8545   │ TCP      │ JSON-RPC HTTP — used by wallets, dApps, MetaMask   │
  │  8546   │ TCP      │ WebSocket — real-time subscriptions (eth_subscribe) │
  │ 30303   │ TCP+UDP  │ P2P network — peer discovery and block propagation  │
  │    80   │ TCP      │ Nginx reverse proxy (HTTP → RPC)                    │
  │   443   │ TCP      │ Nginx HTTPS (only when --domain is used)            │
  │    22   │ TCP      │ SSH — protected by Fail2Ban                         │
  └─────────┴──────────┴─────────────────────────────────────────────────────┘

CHANGING PORTS
  To use non-default ports, pass flags at install time:
    sudo bash setup-fullnode-server.sh --dashboard-port 8080 --rpc-port 9545 --ws-port 9546 --p2p-port 30304

  To change ports on an existing install (update mode):
    sudo bash setup-fullnode-server.sh --update --dashboard-port 8080 --rpc-port 9545

FIREWALL (UFW — Ubuntu/Debian)
  Allow a port:   ufw allow <PORT>/tcp
  Allow P2P UDP:  ufw allow 30303/udp
  Deny a port:    ufw deny <PORT>/tcp
  Check status:   ufw status numbered

FIREWALL (firewalld — CentOS/RHEL/AlmaLinux)
  Allow a port:   firewall-cmd --permanent --add-port=<PORT>/tcp && firewall-cmd --reload
  Remove a port:  firewall-cmd --permanent --remove-port=<PORT>/tcp && firewall-cmd --reload
  Check status:   firewall-cmd --list-all

ADDING A DOMAIN + HTTPS
  sudo bash setup-fullnode-server.sh --domain rpc.yourdomain.com
  (Your domain must already point to this server's IP before running)

EXAMPLES
  Fresh install (Docker, default ports):
    sudo bash setup-fullnode-server.sh

  Fresh install (native service, custom ports):
    sudo bash setup-fullnode-server.sh --no-docker --dashboard-port 8080 --rpc-port 9545 --p2p-port 30304

  With domain + TLS:
    sudo bash setup-fullnode-server.sh --domain rpc.example.com

  Update existing install:
    sudo bash setup-fullnode-server.sh --update

  Remove everything:
    sudo bash setup-fullnode-server.sh --uninstall

EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --datadir)    GYDS_DATADIR="$2";  shift 2 ;;
    --dashboard-port) GYDS_DASHBOARD_PORT="$2"; shift 2 ;;
    --rpc-port)   GYDS_RPC_PORT="$2"; shift 2 ;;
    --ws-port)    GYDS_WS_PORT="$2";  shift 2 ;;
    --p2p-port)   GYDS_P2P_PORT="$2"; shift 2 ;;
    --ssh-port)   SSH_PORT="$2";      shift 2 ;;
    --domain)     DOMAIN="$2";        shift 2 ;;
    --log-level)  LOG_LEVEL="$2";     shift 2 ;;
    --bootstrap-nodes) BOOTSTRAP_NODES="$2"; shift 2 ;;
    --allow-ip)        ALLOW_IPS="${ALLOW_IPS} $2"; shift 2 ;;
    --disk-alert)      DISK_ALERT_GB="$2";  shift 2 ;;
    --rebuild)         FORCE_REBUILD=true;  shift   ;;
    --no-docker)       USE_DOCKER=false;    shift   ;;
    --no-fail2ban)     USE_FAIL2BAN=false;  shift   ;;
    --update)          IS_UPDATE=true;      shift   ;;
    --uninstall)       UNINSTALL=true;      shift   ;;
    --help|-h)         show_help; exit 0   ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

# systemd's ReadWritePaths= directive only accepts absolute paths. Keep the
# runtime data directory unambiguous when an operator passes a relative path
# such as ./data, and make the resolved value flow into .env, Docker, and the
# native service alike.
if [[ "$GYDS_DATADIR" != /* ]]; then
  _original_datadir="$GYDS_DATADIR"
  if command -v realpath >/dev/null 2>&1; then
    GYDS_DATADIR="$(realpath -m -- "${APP_DIR}/${GYDS_DATADIR}")"
  else
    GYDS_DATADIR="${APP_DIR%/}/${GYDS_DATADIR#./}"
  fi
  echo "[INFO] Resolved relative data directory '${_original_datadir}' to '${GYDS_DATADIR}'"
fi

[[ "$GYDS_DATADIR" != *$'\n'* && "$GYDS_DATADIR" != *$'\r'* ]] \
  || { echo "[ERROR] Data directory contains a newline, which is invalid." >&2; exit 1; }
[[ "$GYDS_DATADIR" != *[[:space:]]* ]] \
  || { echo "[ERROR] Data directory cannot contain whitespace: ${GYDS_DATADIR}" >&2; exit 1; }

validate_port() {
  local name="$1" value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] && (( value >= 1 && value <= 65535 )) \
    || die "${name}=${value} is not a valid TCP port (must be 1-65535)."
}

validate_port "GYDS_DASHBOARD_PORT" "$GYDS_DASHBOARD_PORT"
validate_port "GYDS_RPC_PORT" "$GYDS_RPC_PORT"
validate_port "GYDS_WS_PORT" "$GYDS_WS_PORT"
validate_port "GYDS_P2P_PORT" "$GYDS_P2P_PORT"
if [[ "$GYDS_DASHBOARD_PORT" == "$GYDS_RPC_PORT" ||
      "$GYDS_DASHBOARD_PORT" == "$GYDS_WS_PORT" ||
      "$GYDS_RPC_PORT" == "$GYDS_WS_PORT" ]]; then
  die "Dashboard, RPC, and WebSocket ports must be different."
fi

# ── Logging helpers ────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[GYDS]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
info() { echo -e "${CYAN}[INFO]${NC} $*"; }
die()  { echo -e "${RED}[ERROR]${NC} $*" >&2; exit 1; }

[[ $EUID -ne 0 ]] && die "Run as root: sudo bash $0"

# ── Uninstall ─────────────────────────────────────────────────────────────────
if $UNINSTALL; then
  log "Uninstalling GYDS fullnode..."
  systemctl stop gyds-fullnode    2>/dev/null || true
  systemctl disable gyds-fullnode 2>/dev/null || true
  rm -f /etc/systemd/system/gyds-fullnode.service
  systemctl daemon-reload 2>/dev/null || true

  if command -v docker &>/dev/null && [ -f "$APP_DIR/docker-compose.yml" ]; then
    docker compose -f "$APP_DIR/docker-compose.yml" down --remove-orphans --volumes 2>/dev/null || true
  fi

  rm -rf "$APP_DIR"
  rm -f /etc/nginx/sites-enabled/gyds-fullnode
  rm -f /etc/nginx/sites-available/gyds-fullnode
  rm -f /etc/nginx/conf.d/gyds-fullnode.conf
  rm -f /etc/nginx/conf.d/gyds-ratelimit.conf
  nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null || true

  rm -f /usr/local/bin/gyds-fullnode-health
  rm -f /usr/local/bin/gyds-fullnode
  { crontab -l 2>/dev/null || true; } | grep -v gyds-fullnode-health | crontab - 2>/dev/null || true

  if id "$APP_USER" &>/dev/null; then
    userdel "$APP_USER" 2>/dev/null || true
  fi

  log "Uninstall complete. Chain data at ${GYDS_DATADIR} was preserved."
  log "To also remove chain data: rm -rf ${GYDS_DATADIR}"
  exit 0
fi

# ── OS detection ──────────────────────────────────────────────────────────────
OS_ID=""
OS_ID_LIKE=""
OS_VERSION_ID=""

if [ -f /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-unknown}"
  OS_ID_LIKE="${ID_LIKE:-}"
  OS_VERSION_ID="${VERSION_ID:-}"
else
  die "Cannot detect OS (/etc/os-release not found). Supported: Ubuntu, Debian, CentOS, RHEL, AlmaLinux, Rocky Linux, Fedora."
fi

is_debian_based() {
  case "$OS_ID" in
    ubuntu|debian|linuxmint|pop|elementary|zorin) return 0 ;;
  esac
  [[ "$OS_ID_LIKE" == *debian* || "$OS_ID_LIKE" == *ubuntu* ]] && return 0
  return 1
}

is_rhel_based() {
  case "$OS_ID" in
    rhel|centos|fedora|almalinux|rocky|ol|amzn) return 0 ;;
  esac
  [[ "$OS_ID_LIKE" == *rhel* || "$OS_ID_LIKE" == *centos* || "$OS_ID_LIKE" == *fedora* ]] && return 0
  return 1
}

is_debian_based || is_rhel_based || die "Unsupported OS: $OS_ID. Supported: Ubuntu, Debian, CentOS, RHEL, AlmaLinux, Rocky, Fedora."

log "Detected OS: $OS_ID $OS_VERSION_ID"

# ── Architecture detection ────────────────────────────────────────────────────
detect_arch() {
  case "$(uname -m)" in
    x86_64)          echo "amd64" ;;
    aarch64|arm64)   echo "arm64" ;;
    armv7l)          echo "armv6l" ;;
    *) die "Unsupported architecture: $(uname -m)" ;;
  esac
}
ARCH="$(detect_arch)"

# ── Package manager wrappers ──────────────────────────────────────────────────
PKG_MANAGER=""
if is_debian_based; then
  export DEBIAN_FRONTEND=noninteractive
  PKG_MANAGER="apt"
elif is_rhel_based; then
  command -v dnf &>/dev/null && PKG_MANAGER="dnf" || PKG_MANAGER="yum"
fi

pkg_update() {
  case "$PKG_MANAGER" in
    apt) apt-get update -qq ;;
    dnf) dnf check-update -q || true ;;
    yum) yum check-update -q || true ;;
  esac
}

pkg_install() {
  case "$PKG_MANAGER" in
    apt) apt-get install -y --no-install-recommends "$@" ;;
    dnf) dnf install -y "$@" ;;
    yum) yum install -y "$@" ;;
  esac
}

# ── Pre-flight checks ─────────────────────────────────────────────────────────
log "Running pre-flight checks..."

MIN_CORES=2
MIN_RAM_MB=2048
MIN_DISK_GB=20

CORES=$(nproc 2>/dev/null || echo 1)
RAM_MB=$(awk '/MemTotal/ {printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo 0)
DISK_KB=$(df -k "$(dirname "$GYDS_DATADIR" 2>/dev/null || echo /)" 2>/dev/null | awk 'NR==2{print $4}' || echo 0)
DISK_GB=$(( DISK_KB / 1024 / 1024 ))

if (( CORES < MIN_CORES )); then
  warn "Only ${CORES} CPU core(s) detected; ${MIN_CORES} recommended for stable block production."
fi
if (( RAM_MB < MIN_RAM_MB )); then
  warn "Only ${RAM_MB} MB RAM detected; ${MIN_RAM_MB} MB recommended."
fi
if (( DISK_GB < MIN_DISK_GB )); then
  die "Insufficient disk space: ${DISK_GB} GB free, ${MIN_DISK_GB} GB required."
fi

info "Pre-flight: ${CORES} cores | ${RAM_MB} MB RAM | ${DISK_GB} GB free disk — OK"

# ── Swap (auto-create if RAM < 2 GB and no swap exists) ───────────────────────
SWAP_TOTAL=$(free -m 2>/dev/null | awk '/^Swap:/{print $2}' || echo "0")
SWAP_TOTAL="${SWAP_TOTAL:-0}"
if (( RAM_MB < 2048 && SWAP_TOTAL == 0 )); then
  if [ -f /swapfile ]; then
    log "Swapfile already exists — re-activating..."
    swapon /swapfile 2>/dev/null || true
  else
    log "Low RAM detected and no swap — creating 2 GB swapfile..."
    fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048 status=none
    chmod 600 /swapfile
    mkswap /swapfile
    swapon /swapfile
    grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  fi
  log "Swap: $(free -h | awk '/^Swap:/{print $2}')"
fi

# ── System packages ───────────────────────────────────────────────────────────
log "Updating system packages..."
pkg_update

log "Installing base dependencies..."
if is_debian_based; then
  pkg_install curl wget git build-essential ca-certificates \
    nginx jq lsof ufw net-tools gnupg software-properties-common certbot python3-certbot-nginx
elif is_rhel_based; then
  if ! rpm -q epel-release &>/dev/null; then
    pkg_install epel-release || warn "EPEL not available — some packages may be missing"
  fi
  pkg_install curl wget git gcc make ca-certificates \
    nginx jq lsof firewalld net-tools gnupg2 certbot python3-certbot-nginx
fi

# Fail2ban is optional defense-in-depth. Do not let a package repository
# or service failure prevent the node and firewall from being installed.
if $USE_FAIL2BAN; then
  if command -v fail2ban-server &>/dev/null; then
    log "Fail2ban already installed"
  elif pkg_install fail2ban; then
    log "Fail2ban installed"
  else
    warn "Fail2ban could not be installed — continuing with firewall protection only."
    USE_FAIL2BAN=false
  fi
else
  warn "Fail2ban installation skipped by --no-fail2ban."
fi

# ── Go installation ───────────────────────────────────────────────────────────
install_go() {
  local target="/usr/local/go"
  log "Installing Go ${GO_VERSION} (${ARCH})..."
  local url="https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz"
  wget -q "$url" -O /tmp/go.tar.gz || die "Failed to download Go from $url"
  rm -rf "$target"
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm -f /tmp/go.tar.gz
  ln -sf /usr/local/go/bin/go    /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  chmod +x /etc/profile.d/go.sh
}

export PATH="$PATH:/usr/local/go/bin"
if ! command -v go &>/dev/null; then
  install_go
else
  CURRENT_GO="$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')"
  if [[ "$CURRENT_GO" != "$GO_VERSION" ]]; then
    warn "Found Go $CURRENT_GO, upgrading to $GO_VERSION..."
    install_go
  fi
fi
log "Go: $(go version)"

# ── Docker installation (optional) ────────────────────────────────────────────
install_docker() {
  if command -v docker &>/dev/null; then
    log "Docker already installed: $(docker --version)"
    return 0
  fi
  log "Installing Docker..."
  if is_debian_based; then
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL "https://download.docker.com/linux/${OS_ID}/gpg" \
      | gpg --dearmor -o /etc/apt/keyrings/docker.gpg 2>/dev/null || \
      curl -fsSL "https://download.docker.com/linux/ubuntu/gpg" \
      | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    local codename="${VERSION_CODENAME:-$(lsb_release -cs 2>/dev/null || echo jammy)}"
    local repo_os="$OS_ID"
    [[ "$OS_ID" != "ubuntu" && "$OS_ID" != "debian" ]] && repo_os="ubuntu"
    echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.gpg] \
https://download.docker.com/linux/${repo_os} ${codename} stable" \
      > /etc/apt/sources.list.d/docker.list
    apt-get update -qq
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
  elif is_rhel_based; then
    local repo_os="centos"
    [[ "$OS_ID" == "fedora" ]] && repo_os="fedora"
    pkg_install yum-utils 2>/dev/null || true
    if command -v dnf &>/dev/null; then
      dnf config-manager --add-repo \
        "https://download.docker.com/linux/${repo_os}/docker-ce.repo" 2>/dev/null || \
        warn "Could not add Docker repo; trying manual install"
    fi
    pkg_install docker-ce docker-ce-cli containerd.io docker-compose-plugin
  fi
  systemctl enable --now docker
  log "Docker: $(docker --version)"
}

if $USE_DOCKER; then
  install_docker
fi

# ── System user ───────────────────────────────────────────────────────────────
if ! id "$APP_USER" &>/dev/null; then
  useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER" \
    || adduser --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
fi
if $USE_DOCKER && command -v docker &>/dev/null; then
  usermod -aG docker "$APP_USER"
fi

# ── Firewall ──────────────────────────────────────────────────────────────────
log "Configuring firewall..."
if is_debian_based && command -v ufw &>/dev/null; then
  ufw default deny incoming
  ufw default allow outgoing
  ufw allow "$SSH_PORT"/tcp
  ufw allow 80/tcp
  ufw allow 443/tcp
  ufw allow "$GYDS_DASHBOARD_PORT"/tcp
  ufw allow "$GYDS_RPC_PORT"/tcp
  ufw allow "$GYDS_WS_PORT"/tcp
  ufw allow "$GYDS_P2P_PORT"/tcp
  ufw allow "$GYDS_P2P_PORT"/udp
  ufw --force enable
  log "UFW firewall enabled"
elif is_rhel_based && command -v firewall-cmd &>/dev/null; then
  systemctl enable --now firewalld
  firewall-cmd --permanent --set-default-zone=public
  firewall-cmd --permanent --add-port="$SSH_PORT"/tcp
  firewall-cmd --permanent --add-port=80/tcp
  firewall-cmd --permanent --add-port=443/tcp
  firewall-cmd --permanent --add-port="$GYDS_DASHBOARD_PORT"/tcp
  firewall-cmd --permanent --add-port="$GYDS_RPC_PORT"/tcp
  firewall-cmd --permanent --add-port="$GYDS_WS_PORT"/tcp
  firewall-cmd --permanent --add-port="$GYDS_P2P_PORT"/tcp
  firewall-cmd --permanent --add-port="$GYDS_P2P_PORT"/udp
  firewall-cmd --reload
  log "firewalld configured"
else
  warn "No supported firewall found (ufw/firewalld). Configure firewall manually."
fi

# ── Fail2Ban ──────────────────────────────────────────────────────────────────
if $USE_FAIL2BAN && command -v fail2ban-server &>/dev/null; then
  mkdir -p /etc/fail2ban/jail.d /etc/fail2ban/filter.d

  # SSH jail — write to jail.d so we never overwrite user's jail.local
  cat > /etc/fail2ban/jail.d/gyds-sshd.local <<EOF
[sshd]
enabled  = true
port     = $SSH_PORT
bantime  = 1h
findtime = 10m
maxretry = 5
EOF

  # Admin-login jail — watches nginx access log for 401/429 on /admin/login
  cat > /etc/fail2ban/filter.d/gyds-admin.conf <<EOF
[Definition]
failregex = ^<HOST> .+ "POST /admin/login .+" (401|429) .+$
ignoreregex =
EOF

  cat > /etc/fail2ban/jail.d/gyds-admin.local <<EOF
[gyds-admin]
enabled  = true
port     = http,https,$GYDS_DASHBOARD_PORT
filter   = gyds-admin
logpath  = $GYDS_DATADIR/access.log
bantime  = 1h
findtime = 5m
maxretry = 5
EOF

  if systemctl enable --now fail2ban && fail2ban-client reload 2>/dev/null; then
    log "Fail2Ban configured (SSH + admin-login jails)"
  else
    warn "Fail2ban could not start — continuing with firewall protection only."
    USE_FAIL2BAN=false
  fi
else
  warn "Fail2ban unavailable/skipped — firewall protection remains active."
fi

# ── Kernel / network tuning ───────────────────────────────────────────────────
log "Applying kernel network tuning for P2P workloads..."
cat > /etc/sysctl.d/99-gyds-fullnode.conf <<EOF
# Increase socket buffer sizes for high-throughput P2P
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.core.rmem_default = 65536
net.core.wmem_default = 65536
net.ipv4.tcp_rmem = 4096 87380 134217728
net.ipv4.tcp_wmem = 4096 65536 134217728

# Allow more concurrent connections
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535

# Reduce TIME_WAIT pressure from many short-lived RPC connections
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_fin_timeout = 15

# Increase max open file descriptors
fs.file-max = 2097152
EOF
sysctl -p /etc/sysctl.d/99-gyds-fullnode.conf --quiet 2>/dev/null || true
log "Kernel tuning applied"

# ── Clone / update / rebuild repo ─────────────────────────────────────────────
log "Setting up application directory..."

# Detect whether the node is already installed
ALREADY_INSTALLED=false
[ -d "$APP_DIR/.git" ] && ALREADY_INSTALLED=true

if $FORCE_REBUILD && $ALREADY_INSTALLED; then
  log "--rebuild requested — stopping service and wiping code directory..."
  log "(Chain data at ${GYDS_DATADIR} is NOT touched)"
  systemctl stop gyds-fullnode 2>/dev/null || true
  if command -v docker &>/dev/null && [ -f "$APP_DIR/docker-compose.yml" ]; then
    docker compose -f "$APP_DIR/docker-compose.yml" down --remove-orphans 2>/dev/null || true
  fi
  # Preserve .env so config survives the rebuild
  ENV_BACKUP=""
  if [ -f "$APP_DIR/.env" ]; then
    ENV_BACKUP=$(mktemp)
    cp "$APP_DIR/.env" "$ENV_BACKUP"
    log "Saved existing .env to $ENV_BACKUP"
  fi
  rm -rf "$APP_DIR"
  ALREADY_INSTALLED=false
fi

mkdir -p "$APP_DIR"

if ! $ALREADY_INSTALLED; then
  log "Cloning repository (fresh)..."
  rm -rf "${APP_DIR:?}"/* "${APP_DIR:?}"/.[!.]* 2>/dev/null || true
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$APP_DIR" \
    || die "git clone failed — check network access and REPO_URL"
  # Restore preserved .env after a rebuild
  if [ -n "${ENV_BACKUP:-}" ] && [ -f "$ENV_BACKUP" ]; then
    cp "$ENV_BACKUP" "$APP_DIR/.env"
    rm -f "$ENV_BACKUP"
    log "Restored .env from backup"
  fi
else
  log "Repository already present — pulling latest from origin/${BRANCH}..."
  git config --global --add safe.directory "$APP_DIR" 2>/dev/null || true
  git -C "$APP_DIR" fetch --depth 1 origin "$BRANCH" \
    || warn "git fetch failed — building from existing code"
  git -C "$APP_DIR" reset --hard "origin/$BRANCH" \
    || warn "git reset failed — building from existing code"
fi

chown -R "$APP_USER:$APP_USER" "$APP_DIR"

# ── Backup before update ──────────────────────────────────────────────────────
if $IS_UPDATE; then
  BACKUP_DIR="/var/backups/gyds-fullnode/$(date +%Y%m%d_%H%M%S)"
  log "Creating backup at ${BACKUP_DIR}..."
  mkdir -p "$BACKUP_DIR"
  [ -f "$APP_DIR/.env" ] && cp "$APP_DIR/.env" "$BACKUP_DIR/.env.bak"
  # Back up the LevelDB state database (state.db) and any keystore files
  if [ -d "${GYDS_DATADIR}/state.db" ]; then
    tar -czf "$BACKUP_DIR/state.db.tar.gz" -C "$GYDS_DATADIR" state.db 2>/dev/null || \
      warn "Could not backup state.db — continuing anyway"
  fi
  if [ -d "${GYDS_DATADIR}/keystore" ]; then
    tar -czf "$BACKUP_DIR/keystore.tar.gz" -C "$GYDS_DATADIR" keystore 2>/dev/null || \
      warn "Could not backup keystore — continuing anyway"
  fi
  log "Backup complete: ${BACKUP_DIR}"
fi

# ── Environment file ──────────────────────────────────────────────────────────
log "Creating environment configuration..."
if [ ! -f "$APP_DIR/.env" ] || $IS_UPDATE; then
  if [ -f "$APP_DIR/.env.example" ]; then
    cp "$APP_DIR/.env.example" "$APP_DIR/.env"
  else
    cat > "$APP_DIR/.env" <<EOF
GYDS_CHAIN_ID=$GYDS_CHAIN_ID
GYDS_NODE_MODE=$GYDS_NODE_MODE
GYDS_DASHBOARD_PORT=$GYDS_DASHBOARD_PORT
GYDS_RPC_PORT=$GYDS_RPC_PORT
GYDS_RPC_HOST=0.0.0.0
GYDS_WS_PORT=$GYDS_WS_PORT
GYDS_P2P_PORT=$GYDS_P2P_PORT
GYDS_DATA_DIR=$GYDS_DATADIR
GYDS_LOG_LEVEL=$LOG_LEVEL
EOF
  fi
  if grep -q '^GYDS_DASHBOARD_PORT=' "$APP_DIR/.env"; then
    sed -i "s|^GYDS_DASHBOARD_PORT=.*|GYDS_DASHBOARD_PORT=$GYDS_DASHBOARD_PORT|" "$APP_DIR/.env"
  else
    printf 'GYDS_DASHBOARD_PORT=%s\n' "$GYDS_DASHBOARD_PORT" >> "$APP_DIR/.env"
  fi
  sed -i "s|^GYDS_RPC_PORT=.*|GYDS_RPC_PORT=$GYDS_RPC_PORT|"   "$APP_DIR/.env"
  sed -i "s|^GYDS_P2P_PORT=.*|GYDS_P2P_PORT=$GYDS_P2P_PORT|"   "$APP_DIR/.env"
  sed -i "s|^GYDS_DATA_DIR=.*|GYDS_DATA_DIR=$GYDS_DATADIR|"     "$APP_DIR/.env"
  sed -i "s|^GYDS_NODE_MODE=.*|GYDS_NODE_MODE=$GYDS_NODE_MODE|"  "$APP_DIR/.env"
  sed -i "s|^GYDS_LOG_LEVEL=.*|GYDS_LOG_LEVEL=$LOG_LEVEL|"      "$APP_DIR/.env"
  if [ -n "$BOOTSTRAP_NODES" ]; then
    grep -q '^GYDS_BOOTSTRAP_NODES=' "$APP_DIR/.env" \
      && sed -i "s|^GYDS_BOOTSTRAP_NODES=.*|GYDS_BOOTSTRAP_NODES=$BOOTSTRAP_NODES|" "$APP_DIR/.env" \
      || echo "GYDS_BOOTSTRAP_NODES=$BOOTSTRAP_NODES" >> "$APP_DIR/.env"
    log "Bootstrap peers configured: $BOOTSTRAP_NODES"
  fi
  chmod 640 "$APP_DIR/.env"
  chown "root:$APP_USER" "$APP_DIR/.env"
fi

# ── Data directories ──────────────────────────────────────────────────────────
# state.db/ is created automatically by LevelDB on first run — do not pre-create it.
# keystore/ holds encrypted account keys; logs/ holds node and health-check logs.
log "Creating data directories..."
mkdir -p "${GYDS_DATADIR}"/{keystore,logs}
chown -R "$APP_USER:$APP_USER" "$GYDS_DATADIR"
chmod 750 "$GYDS_DATADIR"

# ── Build binary (only when not using Docker) ─────────────────────────────────
if ! $USE_DOCKER; then
  log "Building gyds-fullnode binary..."
  cd "$APP_DIR"
  export HOME="/root"
  export GOTOOLCHAIN=local
  mkdir -p "$APP_DIR/bin"
  # GONOSUMDB=* allows go mod tidy to fetch module checksums without
  # requiring GOSUM verification — safe for private/air-gapped environments.
  GONOSUMDB="*" go mod tidy
  go build -ldflags="-s -w -X main.version=1.0.0" -o bin/gyds-fullnode .
  chown "$APP_USER:$APP_USER" "$APP_DIR/bin/gyds-fullnode"
  log "Binary built: $(file "$APP_DIR/bin/gyds-fullnode")"
fi

# ── Docker Compose (optional) ─────────────────────────────────────────────────
if $USE_DOCKER; then
  log "Starting Docker container..."
  cd "$APP_DIR"
  DOCKER_BIN="$(command -v docker)"

  # Do not allow an old native service to compete for the same ports.
  systemctl disable --now gyds-fullnode 2>/dev/null || true

  docker compose down --remove-orphans 2>/dev/null || true
  if $IS_UPDATE || $ALREADY_INSTALLED; then
    # Pull new code was already done above — rebuild without wiping layer cache
    docker compose build
  else
    # Fresh install: force a clean image build
    docker compose build --no-cache
  fi
  docker compose up -d
  log "Docker container started"

  # Manage Compose itself with systemd so Docker and the node return after
  # reboot, and so the update helper can stop/start one stable unit.
  cat > /etc/systemd/system/gyds-fullnode-compose.service <<EOF
[Unit]
Description=GYDS Chain Full Node (Docker Compose)
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=${APP_DIR}
ExecStart=/usr/bin/docker compose up -d
ExecStop=${DOCKER_BIN} compose down
RemainAfterExit=yes
TimeoutStartSec=0
TimeoutStopSec=120

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  sed -i "s|^ExecStart=.*|ExecStart=${DOCKER_BIN} compose up -d|" /etc/systemd/system/gyds-fullnode-compose.service
  systemctl daemon-reload
  systemctl enable --now gyds-fullnode-compose.service
  log "Docker Compose service enabled for boot: gyds-fullnode-compose.service"
fi

# ── Nginx reverse proxy ───────────────────────────────────────────────────────
log "Configuring Nginx reverse proxy..."

cat > /etc/nginx/conf.d/gyds-ratelimit.conf <<EOF
limit_req_zone \$binary_remote_addr zone=gyds_rpc:10m rate=30r/s;
limit_conn_zone \$binary_remote_addr zone=gyds_conn:10m;
EOF

NGINX_SERVER_NAME="${DOMAIN:-_}"

# Build IP allowlist block — if no IPs specified, allow all
NGINX_IP_RULES=""
if [ -n "$ALLOW_IPS" ]; then
  for ip in $ALLOW_IPS; do
    NGINX_IP_RULES="${NGINX_IP_RULES}    allow ${ip};
"
  done
  NGINX_IP_RULES="${NGINX_IP_RULES}    deny all;"
  log "RPC access restricted to:${ALLOW_IPS}"
fi

if is_debian_based; then
  rm -f /etc/nginx/sites-enabled/default
  cat > /etc/nginx/sites-available/gyds-fullnode <<EOF
server {
    listen 80;
    server_name ${NGINX_SERVER_NAME};

    limit_req       zone=gyds_rpc burst=60 nodelay;
    limit_conn      gyds_conn 20;
    limit_req_status 429;

${NGINX_IP_RULES}

    # Browser dashboard and its REST/WebSocket API.
    location / {
        proxy_pass         http://127.0.0.1:${GYDS_DASHBOARD_PORT};
        proxy_http_version 1.1;
        proxy_set_header   Upgrade    \$http_upgrade;
        proxy_set_header   Connection "upgrade";
        proxy_set_header   Host       \$host;
        proxy_set_header   X-Real-IP  \$remote_addr;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }

    # Dedicated JSON-RPC remains available without exposing the dashboard
    # listener through the proxy.
    location /json-rpc {
        proxy_pass         http://127.0.0.1:${GYDS_RPC_PORT}/;
        proxy_http_version 1.1;
        proxy_set_header   Host \$host;
        proxy_set_header   X-Real-IP \$remote_addr;
    }
}
EOF
  ln -sf /etc/nginx/sites-available/gyds-fullnode /etc/nginx/sites-enabled/
elif is_rhel_based; then
  cat > /etc/nginx/conf.d/gyds-fullnode.conf <<EOF
server {
    listen 80;
    server_name ${NGINX_SERVER_NAME};

    limit_req       zone=gyds_rpc burst=60 nodelay;
    limit_conn      gyds_conn 20;
    limit_req_status 429;

${NGINX_IP_RULES}

    # Browser dashboard and its REST/WebSocket API.
    location / {
        proxy_pass         http://127.0.0.1:${GYDS_DASHBOARD_PORT};
        proxy_http_version 1.1;
        proxy_set_header   Upgrade    \$http_upgrade;
        proxy_set_header   Connection "upgrade";
        proxy_set_header   Host       \$host;
        proxy_set_header   X-Real-IP  \$remote_addr;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }

    # Dedicated JSON-RPC remains available without exposing the dashboard
    # listener through the proxy.
    location /json-rpc {
        proxy_pass         http://127.0.0.1:${GYDS_RPC_PORT}/;
        proxy_http_version 1.1;
        proxy_set_header   Host \$host;
        proxy_set_header   X-Real-IP \$remote_addr;
    }
}
EOF
fi

nginx -t && systemctl enable --now nginx && systemctl reload nginx
log "Nginx configured"

# ── TLS / HTTPS via Certbot (optional, requires --domain) ─────────────────────
if [ -n "$DOMAIN" ]; then
  if command -v certbot &>/dev/null; then
    log "Provisioning TLS certificate for ${DOMAIN}..."
    certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos \
      --register-unsafely-without-email --redirect 2>/dev/null || \
      warn "Certbot failed — ensure ${DOMAIN} resolves to this server and port 80 is reachable."
    ({ crontab -l 2>/dev/null || true; } | grep -v certbot || true; \
      echo "0 3 * * * certbot renew --quiet --nginx") | crontab - 2>/dev/null || true
    log "TLS certificate provisioned — HTTPS enabled"
  else
    warn "certbot not found — skipping TLS setup"
  fi
fi

# ── Systemd service (native binary only) ──────────────────────────────────────
if ! $USE_DOCKER; then
  # Do not allow an old Docker Compose unit to compete for the same ports.
  systemctl disable --now gyds-fullnode-compose.service 2>/dev/null || true
  log "Creating hardened systemd service..."
  cat > /etc/systemd/system/gyds-fullnode.service <<EOF
[Unit]
Description=GYDS Chain Full Node
Documentation=https://github.com/hc172808/fullnode
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${APP_USER}
Group=${APP_USER}
WorkingDirectory=${APP_DIR}
EnvironmentFile=${APP_DIR}/.env
Environment=GYDS_DATA_DIR=${GYDS_DATADIR}
ExecStart=${APP_DIR}/bin/gyds-fullnode start
Restart=on-failure
RestartSec=10s
TimeoutStopSec=30s

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096

# Logging
StandardOutput=append:${GYDS_DATADIR}/logs/fullnode.log
StandardError=append:${GYDS_DATADIR}/logs/fullnode-error.log

# Systemd hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${GYDS_DATADIR} ${APP_DIR}/bin
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictRealtime=true
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  _rw_paths="$(awk -F= '/^ReadWritePaths=/{print $2}' /etc/systemd/system/gyds-fullnode.service)"
  for _rw_path in $_rw_paths; do
    [[ "$_rw_path" == /* ]] ||
      die "Generated systemd service contains a non-absolute ReadWritePaths entry: ${_rw_path}"
  done
  if command -v systemd-analyze &>/dev/null; then
    systemd-analyze verify /etc/systemd/system/gyds-fullnode.service \
      || die "Generated systemd service is invalid. Review /etc/systemd/system/gyds-fullnode.service."
  fi
  # Smart start: restart if running, start if stopped, enable+start if new
  if systemctl is-active --quiet gyds-fullnode 2>/dev/null; then
    systemctl restart gyds-fullnode
    log "Systemd service restarted"
  elif systemctl is-enabled --quiet gyds-fullnode 2>/dev/null; then
    systemctl start gyds-fullnode
    log "Systemd service started"
  else
    systemctl enable gyds-fullnode
    systemctl start gyds-fullnode
    log "Systemd service enabled and started"
  fi
fi

# Install the Git update helper for both Docker and native deployments.
install -m 0755 "${APP_DIR}/scripts/update-from-git.sh" /usr/local/bin/gyds-fullnode-update
log "Update helper installed: /usr/local/bin/gyds-fullnode-update"

# ── Health check script ────────────────────────────────────────────────────────
log "Installing health check..."
cat > /usr/local/bin/gyds-fullnode-health <<EOF
#!/usr/bin/env bash
RPC_PORT="${GYDS_RPC_PORT}"
DATADIR="${GYDS_DATADIR}"
DISK_ALERT_GB="${DISK_ALERT_GB}"
NOW=\$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ── RPC check ────────────────────────────────────────────────────────────────
RESP=\$(curl -sf --max-time 5 -X POST "http://localhost:\${RPC_PORT}" \\
  -H "Content-Type: application/json" \\
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' 2>/dev/null || true)
if [ -n "\$RESP" ]; then
  echo "[OK]   \${NOW} RPC responsive -- \$RESP"
else
  echo "[WARN] \${NOW} RPC not responding on port \${RPC_PORT}"
  if systemctl is-active --quiet gyds-fullnode 2>/dev/null; then
    systemctl restart gyds-fullnode && echo "[INFO] \${NOW} Service restarted"
  elif command -v docker &>/dev/null; then
    cd ${APP_DIR} && docker compose up -d && echo "[INFO] \${NOW} Container restarted"
  fi
fi

# ── Disk space check ─────────────────────────────────────────────────────────
DISK_FREE_KB=\$(df -k "\${DATADIR}" 2>/dev/null | awk 'NR==2{print \$4}' || echo 0)
DISK_FREE_GB=\$(( DISK_FREE_KB / 1024 / 1024 ))
if (( DISK_FREE_GB < DISK_ALERT_GB )); then
  echo "[WARN] \${NOW} LOW DISK SPACE: \${DISK_FREE_GB} GB free on \${DATADIR} (threshold: \${DISK_ALERT_GB} GB)"
  echo "[WARN] \${NOW} Chain data is growing — consider pruning or expanding disk."
else
  echo "[OK]   \${NOW} Disk: \${DISK_FREE_GB} GB free on \${DATADIR}"
fi
EOF
chmod +x /usr/local/bin/gyds-fullnode-health

# Register cron job (every 5 minutes); safe even when no crontab exists yet
({ crontab -l 2>/dev/null || true; } | grep -v gyds-fullnode-health || true; \
  echo "*/5 * * * * /usr/local/bin/gyds-fullnode-health >> ${GYDS_DATADIR}/logs/health.log 2>&1") \
  | crontab -
log "Health check cron installed"

# ── Log rotation ───────────────────────────────────────────────────────────────
cat > /etc/logrotate.d/gyds-fullnode <<EOF
${GYDS_DATADIR}/logs/*.log {
    daily
    rotate 14
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
}
EOF

# ── Post-install health verification ──────────────────────────────────────────
log "Verifying node is running..."
sleep 5
NODE_OK=false
for i in 1 2 3 4 5; do
  RESP=$(curl -sf --max-time 5 -X POST "http://localhost:${GYDS_RPC_PORT}" \
    -H "Content-Type: application/json" \
    --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' 2>/dev/null || true)
  if [ -n "$RESP" ]; then
    NODE_OK=true
    BLOCK_HEX=$(echo "$RESP" | grep -o '"result":"0x[^"]*"' | grep -o '0x[^"]*' || echo "0x0")
    info "Node is live — current block: ${BLOCK_HEX}"
    break
  fi
  warn "Attempt $i/5: node not yet responsive, waiting 5s..."
  sleep 5
done

if ! $NODE_OK; then
  warn "Node did not respond within 30s. Check logs:"
  if ! $USE_DOCKER; then
    warn "  journalctl -u gyds-fullnode -n 50"
    warn "  tail -f ${GYDS_DATADIR}/logs/fullnode-error.log"
  else
    warn "  cd ${APP_DIR} && docker compose logs --tail=50"
  fi
fi

# Dashboard check — the dashboard is a separate HTTP listener from JSON-RPC.
DASHBOARD_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  --max-time 5 "http://localhost:${GYDS_DASHBOARD_PORT}/" 2>/dev/null || true)
if [[ "$DASHBOARD_CODE" =~ ^(200|301|302)$ ]]; then
  log "Dashboard is live on port ${GYDS_DASHBOARD_PORT}"
else
  warn "Dashboard is not responding on port ${GYDS_DASHBOARD_PORT} (HTTP ${DASHBOARD_CODE:-000})."
  warn "Open: http://${SERVER_IP:-YOUR_SERVER_IP}:${GYDS_DASHBOARD_PORT}/"
fi

# ── Detect public IP ──────────────────────────────────────────────────────────
SERVER_IP=$(curl -sf --max-time 5 https://api.ipify.org 2>/dev/null \
  || curl -sf --max-time 5 https://ifconfig.me 2>/dev/null \
  || hostname -I 2>/dev/null | awk '{print $1}' \
  || echo "YOUR_SERVER_IP")

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "╔════════════════════════════════════════════════════════════════╗"
echo "║              GYDS FULL NODE DEPLOYED                          ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""
if $NODE_OK; then
  echo "  Node status : RUNNING OK"
else
  echo "  Node status : NOT RESPONDING — check logs below"
fi
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ENDPOINTS"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [ -n "$DOMAIN" ]; then
  echo "  JSON-RPC  (HTTPS) : https://${DOMAIN}"
  echo "  WebSocket  (WSS)  : wss://${DOMAIN}/ws"
  echo "  Dashboard (direct): http://${SERVER_IP}:${GYDS_DASHBOARD_PORT}"
  echo "  JSON-RPC (direct) : http://${SERVER_IP}:${GYDS_RPC_PORT}  (bypass nginx)"
else
  echo "  Dashboard (HTTP)  : http://${SERVER_IP}:${GYDS_DASHBOARD_PORT}"
  echo "  JSON-RPC  (HTTP)  : http://${SERVER_IP}:${GYDS_RPC_PORT}"
  echo "  WebSocket  (WS)   : ws://${SERVER_IP}:${GYDS_WS_PORT}"
  echo "  Via Nginx  (HTTP) : http://${SERVER_IP}  (port 80 → dashboard ${GYDS_DASHBOARD_PORT})"
fi
echo "  P2P Network (TCP) : tcp://${SERVER_IP}:${GYDS_P2P_PORT}"
echo "  P2P Network (UDP) : udp://${SERVER_IP}:${GYDS_P2P_PORT}"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  PORT REFERENCE"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Port   Proto    Purpose"
echo "  ─────  ───────  ────────────────────────────────────────────────"
echo "  ${GYDS_DASHBOARD_PORT}   TCP      Dashboard web UI"
echo "  ${GYDS_RPC_PORT}   TCP      JSON-RPC HTTP (wallets, MetaMask, dApps)"
echo "  ${GYDS_WS_PORT}   TCP      WebSocket (real-time block/tx subscriptions)"
echo "  ${GYDS_P2P_PORT}  TCP+UDP  P2P networking (peer discovery, block sync)"
echo "  80     TCP      Nginx reverse proxy → dashboard port ${GYDS_DASHBOARD_PORT}"
if [ -n "$DOMAIN" ]; then
  echo "  443    TCP      Nginx HTTPS (TLS cert: ${DOMAIN})"
fi
echo "  22     TCP      SSH (Fail2Ban active, max 5 attempts/10 min)"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  FIREWALL COMMANDS (UFW)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  View open ports : ufw status numbered"
echo "  Allow a port    : ufw allow <PORT>/tcp"
echo "  Allow P2P UDP   : ufw allow ${GYDS_P2P_PORT}/udp"
echo "  Block a port    : ufw deny <PORT>/tcp"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  HOW TO CHANGE PORTS"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Re-run with --update and the new port flags:"
echo "  sudo bash ${APP_DIR}/setup-fullnode-server.sh \\"
echo "    --update --dashboard-port 8080 --rpc-port 9545 --ws-port 9546 --p2p-port 30304"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  MANAGEMENT"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if $USE_DOCKER; then
  echo "  Managed by : Docker Compose"
  echo "  Status     : cd ${APP_DIR} && docker compose ps"
  echo "  Logs       : cd ${APP_DIR} && docker compose logs -f"
  echo "  Restart    : cd ${APP_DIR} && docker compose restart"
  echo "  Stop       : cd ${APP_DIR} && docker compose down"
else
  echo "  Managed by : systemd"
  echo "  Status     : systemctl status gyds-fullnode"
  echo "  Logs       : journalctl -u gyds-fullnode -f"
  echo "  Log files  : ${GYDS_DATADIR}/logs/fullnode.log"
  echo "  Restart    : systemctl restart gyds-fullnode"
  echo "  Stop       : systemctl stop gyds-fullnode"
fi
echo ""
echo "  Data dir   : ${GYDS_DATADIR}"
echo "  Dashboard  : HTTP port ${GYDS_DASHBOARD_PORT}"
echo "  LevelDB    : ${GYDS_DATADIR}/state.db/   (chain blocks + account state)"
echo "  Keystore   : ${GYDS_DATADIR}/keystore/"
echo "  Logs dir   : ${GYDS_DATADIR}/logs/"
echo "  Health     : /usr/local/bin/gyds-fullnode-health"
if $IS_UPDATE; then
  echo "  Backup     : /var/backups/gyds-fullnode/"
fi
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ADD TO METAMASK / WALLET"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Network Name : GYDS Chain"
echo "  Chain ID     : ${GYDS_CHAIN_ID}"
if [ -n "$DOMAIN" ]; then
  echo "  RPC URL      : https://${DOMAIN}"
  echo "  WebSocket    : wss://${DOMAIN}/ws"
else
  echo "  RPC URL      : http://${SERVER_IP}:${GYDS_RPC_PORT}"
  echo "  WebSocket    : ws://${SERVER_IP}:${GYDS_WS_PORT}"
fi
echo "  Currency     : GYDS"
echo "  Block Expl.  : (none configured)"
echo ""
echo "  In MetaMask: Settings → Networks → Add Network → fill fields above"
echo ""
if [ -n "$BOOTSTRAP_NODES" ]; then
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  BOOTSTRAP PEERS"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ${BOOTSTRAP_NODES}"
echo ""
fi
if [ -n "$ALLOW_IPS" ]; then
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  RPC ACCESS CONTROL"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Allowed IPs : ${ALLOW_IPS}"
echo "  All other IPs are blocked at Nginx"
echo ""
fi
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  QUICK COMMANDS"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Update    : sudo bash ${APP_DIR}/setup-fullnode-server.sh --update"
echo "  Uninstall : sudo bash ${APP_DIR}/setup-fullnode-server.sh --uninstall"
echo "  Port help : sudo bash ${APP_DIR}/setup-fullnode-server.sh --help"
echo "  RPC test  : curl -s -X POST http://localhost:${GYDS_RPC_PORT} \\"
echo "                -H 'Content-Type: application/json' \\"
echo "                -d '{\"jsonrpc\":\"2.0\",\"method\":\"eth_blockNumber\",\"params\":[],\"id\":1}'"
echo ""
