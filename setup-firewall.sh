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
#    --data-dir DIR         Node data directory for access logs
#    --ufw-only             Configure UFW only; do not install/start optional services
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
DATA_DIR="${DATA_DIR:-/var/lib/gyds-fullnode}"
FAIL2BAN_ENABLED=true
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
    --data-dir)       DATA_DIR="$2";       shift 2 ;;
    --no-fail2ban)    FAIL2BAN_ENABLED=false; shift ;;
    --ufw-only)       FAIL2BAN_ENABLED=false; shift ;;
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

if $FAIL2BAN_ENABLED; then
  # ── fail2ban ──────────────────────────────────────────────────
  # Fail2ban is an optional layer. UFW above remains the required
  # network boundary, so a package/repository/service problem must
  # never prevent the node from being deployed.
  log "Installing and configuring fail2ban (optional)..."

  if ! command -v fail2ban-server &>/dev/null; then
    info "fail2ban not found — attempting installation..."
    if command -v apt-get &>/dev/null; then
      DEBIAN_FRONTEND=noninteractive apt-get install -y fail2ban >/dev/null 2>&1 || true
    elif command -v dnf &>/dev/null; then
      dnf install -y fail2ban >/dev/null 2>&1 || true
    elif command -v yum &>/dev/null; then
      yum install -y fail2ban >/dev/null 2>&1 || true
    fi
  fi

  if ! command -v fail2ban-server &>/dev/null; then
    warn "fail2ban is unavailable; continuing with UFW protection only."
    FAIL2BAN_ENABLED=false
  fi
fi

if $FAIL2BAN_ENABLED; then
  # Deploy jail and filter configuration
  if [[ ! -f "${SCRIPT_DIR}/fail2ban/jail.local" ]]; then
    warn "fail2ban/jail.local is missing; continuing with UFW protection only."
    FAIL2BAN_ENABLED=false
  else
    cp "${SCRIPT_DIR}/fail2ban/jail.local" /etc/fail2ban/jail.local
    chmod 644 /etc/fail2ban/jail.local

    # Keep the shipped GYDS jails portable across configured ports and data
    # dirs. Restrict the rewrite to GYDS sections so the SSH jail is untouched.
    _gyds_ports="port           = http,https,${RPC_PORT},${WS_PORT},${DASHBOARD_PORT}"
    _gyds_logpath="logpath        = ${DATA_DIR}/access.log"
    sed -i \
      -e "/^\[gyds-rpc-flood\]/,/^\[/{s|^port[[:space:]]*=.*$|${_gyds_ports}|; s|^logpath[[:space:]]*=.*$|${_gyds_logpath}|}" \
      -e "/^\[gyds-rpc-badrpc\]/,/^\[/{s|^port[[:space:]]*=.*$|${_gyds_ports}|; s|^logpath[[:space:]]*=.*$|${_gyds_logpath}|}" \
      -e "/^\[gyds-scan\]/,/^\[/{s|^port[[:space:]]*=.*$|${_gyds_ports}|; s|^logpath[[:space:]]*=.*$|${_gyds_logpath}|}" \
      /etc/fail2ban/jail.local

    mkdir -p /etc/fail2ban/filter.d
    for _filter in "${SCRIPT_DIR}/fail2ban/filter.d/"*.conf; do
      [[ -f "$_filter" ]] || continue
      cp "$_filter" /etc/fail2ban/filter.d/
      chmod 644 "/etc/fail2ban/filter.d/$(basename "$_filter")"
    done

    # Install the UFW action definition if not already present.
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

    # Validate configuration before (re)starting the service.
    log "Validating fail2ban configuration..."
    if ! fail2ban-client --test 2>/dev/null; then
      warn "fail2ban configuration is invalid; continuing with UFW protection only."
      fail2ban-client --test 2>&1 | head -20 >&2 || true
      FAIL2BAN_ENABLED=false
    else
      log "fail2ban configuration is valid"
    fi
  fi
fi

if $FAIL2BAN_ENABLED; then
  systemctl enable fail2ban >/dev/null 2>&1 || true
  systemctl stop fail2ban 2>/dev/null || true
  sleep 1

  if ! systemctl start fail2ban; then
    warn "fail2ban failed to start; continuing with UFW protection only."
    systemctl status fail2ban --no-pager >&2 || true
    journalctl -u fail2ban -n 50 --no-pager >&2 || true
    FAIL2BAN_ENABLED=false
  fi
fi

if $FAIL2BAN_ENABLED; then
  log "Waiting for fail2ban socket at ${F2B_SOCKET}..."
  _waited=0
  until [[ -S "$F2B_SOCKET" ]]; do
    if [[ $_waited -ge $F2B_WAIT_TIMEOUT ]]; then
      warn "fail2ban socket did not appear; continuing with UFW protection only."
      FAIL2BAN_ENABLED=false
      break
    fi
    sleep 1
    ((_waited++)) || true
  done
fi

if $FAIL2BAN_ENABLED; then
  if ! fail2ban-client status >/dev/null 2>&1; then
    warn "fail2ban-client cannot communicate with the service; continuing with UFW protection only."
    FAIL2BAN_ENABLED=false
  else
    log "fail2ban is running"
  fi
fi

# ── Summary ───────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║        GYDS Full Node — Firewall Hardened            ║"
echo "╚══════════════════════════════════════════════════════╝"
echo "  Ports open:  SSH:${SSH_PORT}  Dashboard:${DASHBOARD_PORT}  RPC:${RPC_PORT}  WS:${WS_PORT}  P2P:${P2P_PORT}"
if $FAIL2BAN_ENABLED; then
  echo "  fail2ban: active (sshd, RPC flood/bad-RPC, scanner, recidive jails)"
else
  echo "  fail2ban: unavailable/skipped (UFW protection remains active)"
fi
echo ""
echo "  Useful commands:"
echo "    Status  : sudo bash setup-firewall.sh --status"
echo "    Unban   : sudo bash setup-firewall.sh --unban <ip>"
echo ""
