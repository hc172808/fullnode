#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════
#  GYDS Chain — Full Node  /  Firewall + fail2ban hardening
#
#  Usage:
#    sudo bash setup-firewall.sh [OPTIONS]
#
#  Options:
#    --ssh-port PORT        SSH port to keep open (default: 22)
#    --dashboard-port PORT  Dashboard web UI port (default: 5000; use 8080 if desired)
#    --rpc-port PORT        JSON-RPC port (default: 8545)
#    --ws-port PORT         WebSocket port (default: 8546)
#    --p2p-port PORT        P2P gossip port (default: 30303)
#    --status               Show active firewall rules and fail2ban bans, then exit
#    --unban IP             Unban an IP address from all fail2ban jails, then exit
#    --help                 Show this help message
# ══════════════════════════════════════════════════════════════
set -euo pipefail
IFS=$'\n\t'

SSH_PORT="${SSH_PORT:-22}"
DASHBOARD_PORT="${DASHBOARD_PORT:-5000}"
RPC_PORT="${RPC_PORT:-8545}"
WS_PORT="${WS_PORT:-8546}"
P2P_PORT="${P2P_PORT:-30303}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

F2B_JAILS=(gyds-rpc-flood gyds-rpc-badrpc gyds-scan sshd recidive)
F2B_SOCKET="/var/run/fail2ban/fail2ban.sock"
F2B_WAIT_TIMEOUT=60

log()   { echo "[$(date '+%H:%M:%S')]  $*"; }
info()  { echo "[$(date '+%H:%M:%S')]  $*"; }
warn()  { echo "[$(date '+%H:%M:%S')] WARNING: $*" >&2; }
error() { echo "[$(date '+%H:%M:%S')] ERROR: $*" >&2; }
die()   { error "$*"; exit 1; }

while [[ $# -gt 0 ]]; do
  case $1 in
    --ssh-port)       SSH_PORT="$2";       shift 2 ;;
    --dashboard-port) DASHBOARD_PORT="$2"; shift 2 ;;
    --rpc-port)       RPC_PORT="$2";       shift 2 ;;
    --ws-port)        WS_PORT="$2";        shift 2 ;;
    --p2p-port)       P2P_PORT="$2";       shift 2 ;;
    --status)
      log "UFW firewall rules:"
      ufw status numbered
      echo ""
      log "fail2ban ban counts:"
      for _j in "${F2B_JAILS[@]}"; do
        echo "  --- ${_j} ---"
        if [[ -S "$F2B_SOCKET" ]]; then
          fail2ban-client status "$_j" 2>/dev/null \
            | grep -E "Banned IP|Currently banned" || echo "  (not loaded)"
        else
          echo "  (fail2ban socket not available)"
        fi
      done
      exit 0 ;;
    --unban)
      [[ $# -lt 2 ]] && die "--unban requires an IP address argument."
      _unban_ip="$2"; shift 2
      if [[ ! -S "$F2B_SOCKET" ]]; then
        die "fail2ban is not running — cannot unban ${_unban_ip}."
      fi
      for _j in "${F2B_JAILS[@]}"; do
        fail2ban-client set "$_j" unbanip "$_unban_ip" 2>/dev/null || true
      done
      ufw delete deny from "$_unban_ip" 2>/dev/null || true
      log "Unbanned ${_unban_ip} from all jails and firewall rules."
      exit 0 ;;
    --help|-h)
      grep '^#  ' "$0" | head -15 | sed 's/^#  \?//'
      exit 0 ;;
    *)
      die "Unknown option: '$1'  Run 'bash setup-firewall.sh --help' for usage." ;;
  esac
done

[[ $EUID -ne 0 ]] && die "This script must be run as root: sudo bash $0"

# ── UFW ───────────────────────────────────────────────────────
log "Configuring UFW firewall..."
command -v ufw &>/dev/null \
  || die "ufw is not installed. Install it with: apt install ufw"

log "SSH port ${SSH_PORT}/tcp will remain open — verifying before enabling firewall."

ufw --force reset
ufw default deny incoming
ufw default allow outgoing

ufw limit   "${SSH_PORT}/tcp"        comment "SSH (rate-limited)"
ufw allow   80/tcp                   comment "HTTP"
ufw allow   443/tcp                  comment "HTTPS"
ufw allow   "${DASHBOARD_PORT}/tcp"  comment "GYDS Dashboard"
ufw allow   "${RPC_PORT}/tcp"        comment "GYDS JSON-RPC"
ufw allow   "${WS_PORT}/tcp"         comment "GYDS WebSocket"
ufw allow   "${P2P_PORT}/tcp"        comment "GYDS P2P (TCP)"
ufw allow   "${P2P_PORT}/udp"        comment "GYDS P2P (UDP)"
ufw allow   51820/udp                comment "WireGuard VPN"

for _blocked in 23 2375 3306 5432 6379 27017; do
  ufw deny "${_blocked}/tcp" comment "Block common attack port" 2>/dev/null || true
done

ufw logging on
ufw --force enable

if ! ufw status | grep -q "Status: active"; then
  die "UFW failed to enable. Check 'ufw status' for details."
fi

log "UFW active"
log "SSH        : port ${SSH_PORT}/tcp  (rate-limited)"
log "Dashboard  : port ${DASHBOARD_PORT}/tcp"
log "JSON-RPC   : port ${RPC_PORT}/tcp"
log "WebSocket  : port ${WS_PORT}/tcp"
log "P2P        : port ${P2P_PORT}/tcp + udp"

# ── Sysctl hardening ──────────────────────────────────────────
log "Applying sysctl network hardening..."
cat > /etc/sysctl.d/99-gyds-fullnode-hardening.conf <<'SYSCTL'
# GYDS Chain Full Node — kernel network hardening
net.ipv4.tcp_syncookies                    = 1
net.ipv4.tcp_max_syn_backlog               = 2048
net.ipv4.conf.all.rp_filter               = 1
net.ipv4.conf.default.rp_filter           = 1
net.ipv4.conf.all.accept_redirects        = 0
net.ipv4.conf.default.accept_redirects    = 0
net.ipv4.conf.all.send_redirects          = 0
net.ipv4.icmp_echo_ignore_broadcasts      = 1
net.ipv4.icmp_ignore_bogus_error_responses = 1
net.ipv4.conf.all.accept_source_route     = 0
net.ipv4.tcp_fin_timeout                  = 15
net.ipv4.tcp_keepalive_time               = 300
net.ipv4.conf.all.log_martians            = 1
SYSCTL
sysctl -p /etc/sysctl.d/99-gyds-fullnode-hardening.conf >/dev/null 2>&1 || true
log "Sysctl hardening applied"

# ── fail2ban ──────────────────────────────────────────────────
log "Installing and configuring fail2ban..."

# Install fail2ban if missing
if ! command -v fail2ban-server &>/dev/null; then
  info "fail2ban not found — installing..."
  if command -v apt-get &>/dev/null; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y fail2ban >/dev/null 2>&1 \
      || die "Failed to install fail2ban via apt-get."
  elif command -v dnf &>/dev/null; then
    dnf install -y fail2ban >/dev/null 2>&1 \
      || die "Failed to install fail2ban via dnf."
  elif command -v yum &>/dev/null; then
    yum install -y fail2ban >/dev/null 2>&1 \
      || die "Failed to install fail2ban via yum."
  else
    die "fail2ban is not installed and could not be installed automatically. Install it manually."
  fi
  log "fail2ban installed"
fi

# Deploy jail and filter configuration
[[ -f "${SCRIPT_DIR}/fail2ban/jail.local" ]] \
  || die "Required file not found: ${SCRIPT_DIR}/fail2ban/jail.local"

cp "${SCRIPT_DIR}/fail2ban/jail.local" /etc/fail2ban/jail.local
chmod 644 /etc/fail2ban/jail.local

mkdir -p /etc/fail2ban/filter.d
for _filter in "${SCRIPT_DIR}/fail2ban/filter.d/"*.conf; do
  [[ -f "$_filter" ]] || continue
  cp "$_filter" /etc/fail2ban/filter.d/
  chmod 644 "/etc/fail2ban/filter.d/$(basename "$_filter")"
done

# Install the UFW action definition if not already present
if ! grep -q "\[Definition\]" /etc/fail2ban/action.d/ufw.conf 2>/dev/null; then
  mkdir -p /etc/fail2ban/action.d
  cat > /etc/fail2ban/action.d/ufw.conf <<'EOF'
[Definition]
actionstart =
actionstop  =
actioncheck =
actionban   = ufw insert 1 deny from <ip> to any
actionunban = ufw delete deny from <ip> to any
EOF
  chmod 644 /etc/fail2ban/action.d/ufw.conf
  log "UFW action definition installed"
fi

# Validate configuration before (re)starting the service
log "Validating fail2ban configuration..."
if ! fail2ban-client --test 2>/dev/null; then
  warn "fail2ban configuration validation failed:"
  fail2ban-client --test 2>&1 | head -20 >&2 || true
  die "Invalid fail2ban configuration. Fix the errors above and retry."
fi
log "fail2ban configuration is valid"

# Enable on boot
systemctl enable fail2ban >/dev/null 2>&1

# Stop any existing instance before restarting with new config
systemctl stop fail2ban 2>/dev/null || true
sleep 1

# Start and attempt automatic recovery on failure
if ! systemctl start fail2ban; then
  error "fail2ban failed to start. Collecting diagnostics..."
  systemctl status fail2ban --no-pager >&2 || true
  journalctl -u fail2ban -n 100 --no-pager >&2 || true

  warn "Attempting automatic recovery with a minimal configuration..."
  cp /etc/fail2ban/jail.local /etc/fail2ban/jail.local.bak 2>/dev/null || true
  cat > /etc/fail2ban/jail.local <<'MINIMAL'
[DEFAULT]
bantime  = 3600
findtime = 300
maxretry = 10
backend  = systemd

[sshd]
enabled = true
MINIMAL

  if ! systemctl start fail2ban; then
    systemctl status fail2ban --no-pager >&2 || true
    journalctl -u fail2ban -n 50 --no-pager >&2 || true
    die "fail2ban could not start even with a minimal configuration. Resolve the errors above."
  fi

  warn "fail2ban started with a minimal configuration (SSH jail only)."
  warn "Restore full config from: ${SCRIPT_DIR}/fail2ban/jail.local"
  warn "Backup of the attempted config saved to: /etc/fail2ban/jail.local.bak"
fi

# Wait until the fail2ban socket appears before issuing any client commands
log "Waiting for fail2ban socket at ${F2B_SOCKET}..."
_waited=0
until [[ -S "$F2B_SOCKET" ]]; do
  if [[ $_waited -ge $F2B_WAIT_TIMEOUT ]]; then
    error "Timed out after ${F2B_WAIT_TIMEOUT}s waiting for fail2ban socket."
    systemctl status fail2ban --no-pager >&2 || true
    journalctl -u fail2ban -n 50 --no-pager >&2 || true
    die "fail2ban did not become ready within ${F2B_WAIT_TIMEOUT} seconds."
  fi
  sleep 1
  (( _waited++ )) || true
done
log "fail2ban socket ready (${_waited}s)"

# Confirm the service is active and the client can communicate
if ! systemctl is-active --quiet fail2ban; then
  die "fail2ban service is unexpectedly inactive after startup."
fi

fail2ban-client status >/dev/null 2>&1 \
  || die "fail2ban-client cannot communicate with the server. Check the socket at ${F2B_SOCKET}."

log "fail2ban is running"

# ── Summary ───────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║        GYDS Full Node — Firewall Hardened            ║"
echo "╚══════════════════════════════════════════════════════╝"
echo "  Ports open:  SSH:${SSH_PORT}  Dashboard:${DASHBOARD_PORT}  RPC:${RPC_PORT}  WS:${WS_PORT}  P2P:${P2P_PORT}"
echo "  fail2ban jails: sshd, gyds-rpc-flood, gyds-rpc-badrpc, gyds-scan, recidive"
echo ""
echo "  Useful commands:"
echo "    Status  : sudo bash setup-firewall.sh --status"
echo "    Unban   : sudo bash setup-firewall.sh --unban <ip>"
echo ""
