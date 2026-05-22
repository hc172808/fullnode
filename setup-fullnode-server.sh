#!/usr/bin/env bash
# ============================================================
# GYDS Chain — Full Node Setup (Ubuntu 22.04 / Debian)
# Usage: sudo bash setup-fullnode-server.sh [--datadir /data/gyds]
# Repo:  https://github.com/hc172808/fullnode
# ============================================================
set -euo pipefail

APP_NAME="gyds-fullnode"
APP_USER="gyds"
APP_DIR="/opt/gyds-fullnode"
REPO_URL="https://github.com/hc172808/fullnode.git"
BRANCH="main"

GYDS_DATADIR="${GYDS_DATADIR:-/var/lib/gyds-fullnode}"
GYDS_CHAIN_ID="${GYDS_CHAIN_ID:-13370}"
GYDS_RPC_PORT="${GYDS_RPC_PORT:-8545}"
GYDS_WS_PORT="${GYDS_WS_PORT:-8546}"
GYDS_P2P_PORT="${GYDS_P2P_PORT:-30303}"
SSH_PORT="22"
GO_VERSION="1.22.4"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --datadir)  GYDS_DATADIR="$2"; shift 2 ;;
    --rpc-port) GYDS_RPC_PORT="$2"; shift 2 ;;
    --p2p-port) GYDS_P2P_PORT="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${GREEN}[GYDS]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
die()  { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

[[ $EUID -ne 0 ]] && die "Run as root: sudo bash $0"
export DEBIAN_FRONTEND=noninteractive

log "Updating system..."
apt-get update -qq && apt-get upgrade -y

log "Installing base packages..."
apt-get install -y --no-install-recommends \
  curl wget git build-essential ca-certificates nginx \
  jq lsof ufw fail2ban net-tools gnupg software-properties-common

log "Installing Go ${GO_VERSION}..."
install_go() {
  ARCH=$(dpkg --print-architecture | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -O /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  rm -f /tmp/go.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
}
if ! command -v go &>/dev/null; then
  install_go
else
  CURRENT="$(go version | awk '{print $3}' | tr -d 'go')"
  [[ "${CURRENT}" != "${GO_VERSION}" ]] && { warn "Upgrading Go..."; install_go; }
fi
export PATH=$PATH:/usr/local/go/bin
log "Go: $(go version)"

log "Installing Docker..."
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
systemctl enable --now docker

id "$APP_USER" &>/dev/null || adduser --disabled-password --gecos "" "$APP_USER"
usermod -aG docker "$APP_USER"

log "Configuring firewall..."
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

log "Configuring Fail2Ban..."
cat > /etc/fail2ban/jail.local <<-EOF
	[DEFAULT]
	bantime = 1h
	findtime = 10m
	maxretry = 5
	[sshd]
	enabled = true
	port = $SSH_PORT
	EOF
systemctl restart fail2ban && systemctl enable fail2ban

log "Cloning repo..."
mkdir -p "$APP_DIR"
if [ ! -d "$APP_DIR/.git" ]; then
  git clone "$REPO_URL" "$APP_DIR"
else
  git -C "$APP_DIR" config --global --add safe.directory "$APP_DIR"
  git -C "$APP_DIR" fetch origin
  git -C "$APP_DIR" reset --hard "origin/$BRANCH"
fi
chown -R "$APP_USER:$APP_USER" "$APP_DIR"

log "Setting up .env..."
[ -f "$APP_DIR/.env.example" ] || die ".env.example not found in repo"
cp "$APP_DIR/.env.example" "$APP_DIR/.env"
chmod 600 "$APP_DIR/.env"
printf '\nGYDS_RPC_PORT=%s\nGYDS_P2P_PORT=%s\nGYDS_DATA_DIR=%s\n' \
  "$GYDS_RPC_PORT" "$GYDS_P2P_PORT" "$GYDS_DATADIR" >> "$APP_DIR/.env"

log "Creating data directories..."
mkdir -p "${GYDS_DATADIR}"/{chaindata,keystore,logs}
chown -R "$APP_USER:$APP_USER" "$GYDS_DATADIR"

log "Building binary..."
cd "$APP_DIR"
make build 2>/dev/null || go build -ldflags="-s -w" -o bin/gyds-fullnode .

log "Building + starting Docker container..."
docker compose down --remove-orphans 2>/dev/null || true
docker compose build --no-cache
docker compose up -d

log "Configuring Nginx..."
rm -f /etc/nginx/sites-enabled/default
cat > /etc/nginx/sites-available/gyds-fullnode <<-NGINX
	server {
	    listen 80;
	    server_name _;
	    location / {
	        proxy_pass http://127.0.0.1:$GYDS_RPC_PORT;
	        proxy_http_version 1.1;
	        proxy_set_header Upgrade \$http_upgrade;
	        proxy_set_header Connection "upgrade";
	        proxy_set_header Host \$host;
	        proxy_set_header X-Real-IP \$remote_addr;
	        proxy_read_timeout 300s;
	    }
	}
	NGINX
ln -sf /etc/nginx/sites-available/gyds-fullnode /etc/nginx/sites-enabled/
nginx -t && systemctl restart nginx && systemctl enable nginx

log "Creating systemd service (native binary)..."
cat > /etc/systemd/system/gyds-fullnode.service <<-SERVICE
	[Unit]
	Description=GYDS Chain Full Node
	After=network-online.target
	Wants=network-online.target
	[Service]
	User=$APP_USER
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

cat > /usr/local/bin/gyds-fullnode-health.sh <<-'EOF'
	#!/usr/bin/env bash
	cd /opt/gyds-fullnode
	docker compose ps | grep -q "Up" || docker compose up -d
	RESP=$(curl -sf -X POST http://localhost:8545 -H "Content-Type: application/json" \
	  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' || true)
	[ -n "$RESP" ] && echo "[OK] $RESP" || echo "[WARN] RPC not responding"
	EOF
chmod +x /usr/local/bin/gyds-fullnode-health.sh
(crontab -l 2>/dev/null | grep -v gyds-fullnode-health; \
 echo "*/5 * * * * /usr/local/bin/gyds-fullnode-health.sh >> /var/log/gyds-fullnode-health.log 2>&1") | crontab -

echo ""
echo "╔══════════════════════════════════════╗"
echo "║      GYDS FULL NODE DEPLOYED         ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo "  JSON-RPC:  http://YOUR_SERVER_IP:$GYDS_RPC_PORT"
echo "  WebSocket: ws://YOUR_SERVER_IP:$GYDS_WS_PORT"
echo "  P2P:       tcp://YOUR_SERVER_IP:$GYDS_P2P_PORT"
echo "  Via Nginx: http://YOUR_SERVER_IP"
echo ""
echo "  Logs:   cd $APP_DIR && docker compose logs -f"
echo "  Health: gyds-fullnode-health.sh"
echo "  Re-run: sudo ./setup-fullnode-server.sh"
echo ""
