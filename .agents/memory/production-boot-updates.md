---
name: Production boot and updates
description: Production startup modes and safe Git-based update behavior
---

Production installs must choose exactly one node runtime: Docker Compose managed by `gyds-fullnode-compose.service`, or native `gyds-fullnode.service`. Both must start after network readiness, while Nginx and available fail2ban start at boot.

**Why:** Running both node runtimes can create port conflicts, and a raw `git pull` can replace code without testing, preserving state, or verifying that the node recovered.

**How to apply:** Use the installed `gyds-fullnode-update` helper. It should lock concurrent updates, reject dirty/non-fast-forward branches, build before stopping the node, back up `.env` and chain state, health-check RPC/dashboard, and retain rollback material.