#!/usr/bin/env bash
# GYDS unattended maintenance: OS security patches plus optional node release updates.
set -euo pipefail

APP_DIR="${GYDS_APP_DIR:-/opt/gyds-fullnode}"
LOG_TAG="gyds-maintenance"

logger -t "$LOG_TAG" "Starting automatic maintenance"

if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get -y upgrade
  # Security updates are applied by the normal Debian/Ubuntu security channel.
  # Do not autoremove packages or reboot automatically.
elif command -v dnf >/dev/null 2>&1; then
  dnf -y upgrade --security || dnf -y upgrade
elif command -v yum >/dev/null 2>&1; then
  yum -y update --security || yum -y update
else
  logger -t "$LOG_TAG" "No supported OS package manager found; skipped OS updates"
fi

# Node updates use the existing fast-forward-only updater, which backs up data,
# tests/builds, health-checks, and rolls back on failure.
if [[ "${GYDS_AUTO_NODE_UPDATES:-true}" == "true" ]] &&
   [[ -x /usr/local/bin/gyds-fullnode-update ]] &&
   [[ -d "$APP_DIR/.git" ]]; then
  /usr/local/bin/gyds-fullnode-update || \
    logger -t "$LOG_TAG" "Node update skipped or rolled back; inspect the update log"
else
  logger -t "$LOG_TAG" "Automatic node release updates disabled or unavailable"
fi

logger -t "$LOG_TAG" "Automatic maintenance finished"