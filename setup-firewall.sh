#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════
#  GYDS Chain — Full Node  /  Firewall + fail2ban hardening
#
#  Usage:
#    sudo bash setup-firewall.sh [options]
#
#  Options:
#    --ssh-port PORT    SSH port (default: 22)
#    --rpc-port PORT    RPC port (default: 8545)
#    --ws-port  PORT    WebSocket port (default: 8546)
#    --p2p-port PORT    P2P port (default: 30303)
#    --status           Show current bans and rules, then exit
#    --unban IP         Unban an IP from all jails, then exit
# ══════════════════════════════════════════════════════════════
set -euo pipefail

SSH_PORT="${SSH_PORT:-22}"
RPC_PORT="${RPC_PORT:-8545}"
WS_PORT="${WS_PORT:-8546}"
P2P_PORT="${P2P_PORT:-30303}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

while [[ $# -gt 0 ]]; do
  case $1 in
    --ssh-port) SSH_PORT="$2"; shift 2 ;;
    --rpc-port) RPC_PORT="$2"; shift 2 ;;
    --ws-port)  WS_PORT="$2";  shift 2 ;;
    --p2p-port) P2P_PORT="$2"; shift 2 ;;
    --status)
      ufw status numbered
      for j in gyds-rpc-flood gyds-rpc-badrpc gyds-scan sshd recidive; do
        echo "--- $j ---" && fail2ban-client status "$j" 2>/dev/null | grep "Banned IP" || true
      done; exit 0 ;;
    --unban)
      IP="$2"; shift 2
      for j in gyds-rpc-flood gyds-rpc-badrpc gyds-scan sshd recidive; do
        fail2ban-client set "$j" unbanip "$IP" 2>/dev/null || true
      done
      ufw delete deny from "$IP" 2>/dev/null || true; exit 0 ;;
    *) shift ;;
  esac
done

log()  { echo "[$(date '+%H:%M:%S')] $*"; }
warn() { echo "[$(date '+%H:%M:%S')] WARNING: $*" >&2; }
[[ $EUID -ne 0 ]] && { echo "Run as root: sudo bash $0"; exit 1; }

# ── UFW ──────────────────────────────────────────────────────
log "Applying UFW firewall rules..."
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw limit   "${SSH_PORT}"/tcp   comment "SSH (rate-limited)"
ufw allow   80/tcp               comment "HTTP"
ufw allow   443/tcp              comment "HTTPS"
ufw allow   "${RPC_PORT}"/tcp    comment "GYDS RPC"
ufw allow   "${WS_PORT}"/tcp     comment "GYDS WebSocket"
ufw allow   "${P2P_PORT}"/tcp    comment "GYDS P2P"
ufw allow   "${P2P_PORT}"/udp    comment "GYDS P2P UDP"
ufw allow   51820/udp            comment "WireGuard VPN"
for port in 23 2375 3306 5432 6379 27017; do
  ufw deny ${port}/tcp comment "Block common attack port" 2>/dev/null || true
done
ufw logging on
ufw --force enable

# ── Sysctl ───────────────────────────────────────────────────
log "Applying sysctl hardening..."
cat > /etc/sysctl.d/99-gyds-fullnode-hardening.conf <<SYSCTL
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 2048
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.ipv4.icmp_ignore_bogus_error_responses = 1
net.ipv4.conf.all.accept_source_route = 0
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_keepalive_time = 300
net.ipv4.conf.all.log_martians = 1
SYSCTL
sysctl -p /etc/sysctl.d/99-gyds-fullnode-hardening.conf

# ── fail2ban ─────────────────────────────────────────────────
log "Configuring fail2ban..."
if ! command -v fail2ban-server &>/dev/null; then
  command -v apt-get &>/dev/null && apt-get install -y fail2ban || \
  command -v dnf     &>/dev/null && dnf    install -y fail2ban || \
  command -v yum     &>/dev/null && yum    install -y fail2ban
fi

cp "${SCRIPT_DIR}/fail2ban/jail.local" /etc/fail2ban/jail.local
mkdir -p /etc/fail2ban/filter.d
for f in "${SCRIPT_DIR}/fail2ban/filter.d/"*.conf; do
  cp "$f" /etc/fail2ban/filter.d/
done

if ! grep -q "\[Definition\]" /etc/fail2ban/action.d/ufw.conf 2>/dev/null; then
  cat > /etc/fail2ban/action.d/ufw.conf <<'EOF'
[Definition]
actionstart =
actionstop  =
actioncheck =
actionban   = ufw insert 1 deny from <ip> to any
actionunban = ufw delete deny from <ip> to any
EOF
fi

systemctl enable fail2ban && systemctl restart fail2ban
sleep 2 && fail2ban-client status

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║        GYDS Full Node — Firewall Hardened            ║"
echo "╚══════════════════════════════════════════════════════╝"
echo "  fail2ban jails: sshd, gyds-rpc-flood, gyds-rpc-badrpc, gyds-scan, recidive"
echo "  Commands: sudo bash setup-firewall.sh --status | --unban <ip>"
