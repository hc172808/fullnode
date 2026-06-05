#!/usr/bin/env bash
# ============================================================
# GYDS Chain — Full Node Setup
# Supports: Ubuntu 20.04/22.04/24.04, Debian 11/12,
#           CentOS Stream 8/9, RHEL 8/9, AlmaLinux 8/9,
#           Rocky Linux 8/9, Fedora 38+
#
# Usage: sudo bash setup-fullnode-server.sh [OPTIONS]
# Options:
#   --datadir  DIR     Chain data directory (default: /var/lib/gyds-fullnode)
#   --rpc-port PORT    RPC port (default: 8545)
#   --ws-port  PORT    WebSocket port (default: 8546)
#   --p2p-port PORT    P2P port (default: 30303)
#   --ssh-port PORT    SSH port for firewall (default: 22)
#   --no-docker        Skip Docker installation (run as native systemd service)
#   --update           Update an existing installation instead of fresh install
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
GYDS_CHAIN_ID="${GYDS_CHAIN_ID:-13370}"
GYDS_RPC_PORT="${GYDS_RPC_PORT:-8545}"
GYDS_WS_PORT="${GYDS_WS_PORT:-8546}"
GYDS_P2P_PORT="${GYDS_P2P_PORT:-30303}"
SSH_PORT="22"
USE_DOCKER=true
IS_UPDATE=false

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --datadir)   GYDS_DATADIR="$2";   shift 2 ;;
    --rpc-port)  GYDS_RPC_PORT="$2";  shift 2 ;;
    --ws-port)   GYDS_WS_PORT="$2";   shift 2 ;;
    --p2p-port)  GYDS_P2P_PORT="$2";  shift 2 ;;
    --ssh-port)  SSH_PORT="$2";       shift 2 ;;
    --no-docker) USE_DOCKER=false;    shift   ;;
    --update)    IS_UPDATE=true;      shift   ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

# ── Logging helpers ────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[GYDS]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
info() { echo -e "${CYAN}[INFO]${NC} $*"; }
die()  { echo -e "${RED}[ERROR]${NC} $*" >&2; exit 1; }

[[ $EUID -ne 0 ]] && die "Run as root: sudo bash $0"

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

# ── System packages ───────────────────────────────────────────────────────────
log "Updating system packages..."
pkg_update

log "Installing base dependencies..."
if is_debian_based; then
  pkg_install curl wget git build-essential ca-certificates \
    nginx jq lsof ufw fail2ban net-tools gnupg software-properties-common
elif is_rhel_based; then
  # Enable EPEL for fail2ban, jq, etc.
  if ! rpm -q epel-release &>/dev/null; then
    pkg_install epel-release || warn "EPEL not available — some packages may be missing"
  fi
  pkg_install curl wget git gcc make ca-certificates \
    nginx jq lsof firewalld fail2ban net-tools gnupg2
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
  ln -sf /usr/local/go/bin/go   /usr/local/bin/go
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
    # Use VERSION_CODENAME if available, else fall back to lsb_release
    local codename="${VERSION_CODENAME:-$(lsb_release -cs 2>/dev/null || echo jammy)}"
    local repo_os="$OS_ID"
    # Debian-derived distros that don't have their own Docker repo use ubuntu's
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
if command -v fail2ban-server &>/dev/null; then
  cat > /etc/fail2ban/jail.local <<-EOF
	[DEFAULT]
	bantime  = 1h
	findtime = 10m
	maxretry = 5

	[sshd]
	enabled = true
	port    = $SSH_PORT
	EOF
  systemctl enable --now fail2ban
  log "Fail2Ban configured"
else
  warn "fail2ban not found — skipping"
fi

# ── Clone / update repo ───────────────────────────────────────────────────────
log "Setting up application directory..."
mkdir -p "$APP_DIR"
if [ ! -d "$APP_DIR/.git" ]; then
  log "Cloning repository..."
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$APP_DIR"
else
  log "Updating repository..."
  git -C "$APP_DIR" fetch --depth 1 origin "$BRANCH"
  git -C "$APP_DIR" reset --hard "origin/$BRANCH"
fi
chown -R "$APP_USER:$APP_USER" "$APP_DIR"

# ── Environment file ──────────────────────────────────────────────────────────
log "Creating environment configuration..."
if [ ! -f "$APP_DIR/.env" ] || $IS_UPDATE; then
  # Use .env.example from repo if available, otherwise generate
  if [ -f "$APP_DIR/.env.example" ]; then
    cp "$APP_DIR/.env.example" "$APP_DIR/.env"
  else
    cat > "$APP_DIR/.env" <<-EOF
	GYDS_CHAIN_ID=$GYDS_CHAIN_ID
	GYDS_NODE_MODE=full
	GYDS_RPC_PORT=$GYDS_RPC_PORT
	GYDS_RPC_HOST=0.0.0.0
	GYDS_WS_PORT=$GYDS_WS_PORT
	GYDS_P2P_PORT=$GYDS_P2P_PORT
	GYDS_DATA_DIR=$GYDS_DATADIR
	GYDS_LOG_LEVEL=info
	EOF
  fi
  # Ensure runtime values are applied (overwrite key lines)
  sed -i "s|^GYDS_RPC_PORT=.*|GYDS_RPC_PORT=$GYDS_RPC_PORT|"   "$APP_DIR/.env"
  sed -i "s|^GYDS_P2P_PORT=.*|GYDS_P2P_PORT=$GYDS_P2P_PORT|"   "$APP_DIR/.env"
  sed -i "s|^GYDS_DATA_DIR=.*|GYDS_DATA_DIR=$GYDS_DATADIR|"     "$APP_DIR/.env"
  sed -i "s|^GYDS_NODE_MODE=.*|GYDS_NODE_MODE=full|"            "$APP_DIR/.env"
  chmod 640 "$APP_DIR/.env"
  chown "root:$APP_USER" "$APP_DIR/.env"
fi

# ── Data directories ──────────────────────────────────────────────────────────
log "Creating data directories..."
mkdir -p "${GYDS_DATADIR}"/{chaindata,keystore,logs}
chown -R "$APP_USER:$APP_USER" "$GYDS_DATADIR"
chmod 750 "$GYDS_DATADIR"

# ── Build binary ──────────────────────────────────────────────────────────────
log "Building gyds-fullnode binary..."
cd "$APP_DIR"
export HOME="/root"
go mod tidy
go build -ldflags="-s -w -X main.version=1.0.0" -o bin/gyds-fullnode .
chown "$APP_USER:$APP_USER" "$APP_DIR/bin/gyds-fullnode"
log "Binary built: $(file "$APP_DIR/bin/gyds-fullnode")"

# ── Docker Compose (optional) ─────────────────────────────────────────────────
if $USE_DOCKER; then
  log "Starting Docker container..."
  docker compose down --remove-orphans 2>/dev/null || true
  docker compose build --no-cache
  docker compose up -d
  log "Docker container started"
fi

# ── Nginx reverse proxy ───────────────────────────────────────────────────────
log "Configuring Nginx reverse proxy..."
if is_debian_based; then
  rm -f /etc/nginx/sites-enabled/default
  cat > /etc/nginx/sites-available/gyds-fullnode <<-NGINX
	server {
	    listen 80;
	    server_name _;

	    # JSON-RPC and WebSocket on the same location
	    location / {
	        proxy_pass         http://127.0.0.1:$GYDS_RPC_PORT;
	        proxy_http_version 1.1;
	        proxy_set_header   Upgrade    \$http_upgrade;
	        proxy_set_header   Connection "upgrade";
	        proxy_set_header   Host       \$host;
	        proxy_set_header   X-Real-IP  \$remote_addr;
	        proxy_read_timeout 300s;
	        proxy_send_timeout 300s;
	    }
	}
	NGINX
  ln -sf /etc/nginx/sites-available/gyds-fullnode /etc/nginx/sites-enabled/
elif is_rhel_based; then
  cat > /etc/nginx/conf.d/gyds-fullnode.conf <<-NGINX
	server {
	    listen 80;
	    server_name _;

	    location / {
	        proxy_pass         http://127.0.0.1:$GYDS_RPC_PORT;
	        proxy_http_version 1.1;
	        proxy_set_header   Upgrade    \$http_upgrade;
	        proxy_set_header   Connection "upgrade";
	        proxy_set_header   Host       \$host;
	        proxy_set_header   X-Real-IP  \$remote_addr;
	        proxy_read_timeout 300s;
	        proxy_send_timeout 300s;
	    }
	}
	NGINX
fi

nginx -t && systemctl enable --now nginx && systemctl reload nginx
log "Nginx configured"

# ── Systemd service (native binary) ──────────────────────────────────────────
# Only create native systemd service when not using Docker
# (Docker has its own restart policy; running both would cause port conflicts)
if ! $USE_DOCKER; then
  log "Creating systemd service..."
  cat > /etc/systemd/system/gyds-fullnode.service <<-SERVICE
	[Unit]
	Description=GYDS Chain Full Node
	Documentation=https://github.com/hc172808/fullnode
	After=network-online.target
	Wants=network-online.target

	[Service]
	Type=simple
	User=$APP_USER
	Group=$APP_USER
	WorkingDirectory=$APP_DIR
	EnvironmentFile=$APP_DIR/.env
	ExecStart=$APP_DIR/bin/gyds-fullnode start
	Restart=on-failure
	RestartSec=10s
	LimitNOFILE=65536
	StandardOutput=append:${GYDS_DATADIR}/logs/fullnode.log
	StandardError=append:${GYDS_DATADIR}/logs/fullnode-error.log

	[Install]
	WantedBy=multi-user.target
	SERVICE

  systemctl daemon-reload
  systemctl enable gyds-fullnode
  systemctl restart gyds-fullnode
  log "systemd service 'gyds-fullnode' enabled and started"
fi

# ── Health check script ───────────────────────────────────────────────────────
log "Installing health check..."
cat > /usr/local/bin/gyds-fullnode-health <<-EOF
	#!/usr/bin/env bash
	RPC_PORT="${GYDS_RPC_PORT}"
	RESP=\$(curl -sf --max-time 5 -X POST "http://localhost:\${RPC_PORT}" \\
	  -H "Content-Type: application/json" \\
	  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' 2>/dev/null || true)
	if [ -n "\$RESP" ]; then
	  echo "[OK]   \$(date -u +%Y-%m-%dT%H:%M:%SZ) RPC responsive — \$RESP"
	else
	  echo "[WARN] \$(date -u +%Y-%m-%dT%H:%M:%SZ) RPC not responding on port \${RPC_PORT}"
	  # Auto-recover
	  if systemctl is-active --quiet gyds-fullnode 2>/dev/null; then
	    systemctl restart gyds-fullnode && echo "[INFO] Service restarted"
	  elif command -v docker &>/dev/null; then
	    cd /opt/gyds-fullnode && docker compose up -d && echo "[INFO] Container restarted"
	  fi
	fi
	EOF
chmod +x /usr/local/bin/gyds-fullnode-health

# Register cron job (runs every 5 minutes)
(crontab -l 2>/dev/null | grep -v gyds-fullnode-health; \
  echo "*/5 * * * * /usr/local/bin/gyds-fullnode-health >> ${GYDS_DATADIR}/logs/health.log 2>&1") \
  | crontab -
log "Health check cron installed"

# ── Log rotation ──────────────────────────────────────────────────────────────
cat > /etc/logrotate.d/gyds-fullnode <<-LOGROTATE
	${GYDS_DATADIR}/logs/*.log {
	    daily
	    rotate 14
	    compress
	    delaycompress
	    missingok
	    notifempty
	    copytruncate
	}
	LOGROTATE

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║          GYDS FULL NODE DEPLOYED                 ║"
echo "╚══════════════════════════════════════════════════╝"
echo ""
echo "  JSON-RPC:  http://YOUR_SERVER_IP:${GYDS_RPC_PORT}"
echo "  WebSocket: ws://YOUR_SERVER_IP:${GYDS_WS_PORT}"
echo "  P2P:       tcp://YOUR_SERVER_IP:${GYDS_P2P_PORT}"
echo "  Via Nginx: http://YOUR_SERVER_IP"
echo ""
echo "  Data dir:  ${GYDS_DATADIR}"
echo "  Logs:      ${GYDS_DATADIR}/logs/"
echo ""
if $USE_DOCKER; then
  echo "  Managed by: Docker Compose"
  echo "  Status:     cd ${APP_DIR} && docker compose ps"
  echo "  Logs:       cd ${APP_DIR} && docker compose logs -f"
else
  echo "  Managed by: systemd"
  echo "  Status:     systemctl status gyds-fullnode"
  echo "  Logs:       journalctl -u gyds-fullnode -f"
  echo "  Log files:  ${GYDS_DATADIR}/logs/fullnode.log"
fi
echo ""
echo "  Health:   /usr/local/bin/gyds-fullnode-health"
echo "  Re-run:   sudo bash ${APP_DIR}/setup-fullnode-server.sh --update"
echo ""
