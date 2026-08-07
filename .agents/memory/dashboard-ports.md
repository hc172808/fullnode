---
name: Dashboard deployment ports
description: Durable deployment rule for the GYDS dashboard listener, container publishing, firewall, and proxy
---

The dashboard is a separate HTTP listener from JSON-RPC. The default dashboard port is 5000, while 8080 is a supported explicit alternative. Deployment scripts must keep the configured dashboard port consistent across the node environment, Docker port publishing, firewall rules, health checks, and reverse-proxy upstream.

**Why:** A prior installer exposed only JSON-RPC and routed Nginx to the RPC listener, so the node could be healthy while the browser dashboard was unreachable.

**How to apply:** When changing deployment ports, update both `setup-fullnode-server.sh` and `deploy.sh`; use `--dashboard-port 8080` for an 8080 deployment and open that same TCP port in the host/cloud firewall.