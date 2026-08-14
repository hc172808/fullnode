---
name: Dashboard deployment ports
description: Durable deployment rule for the GYDS dashboard listener, container publishing, firewall, and proxy
---

The dashboard is a separate HTTP listener from JSON-RPC. The default dashboard port is 5000, while 8080 is a supported explicit alternative. Deployment scripts must keep the configured dashboard port consistent across the node environment, Docker port publishing, firewall rules, health checks, and reverse-proxy upstream.

**Why:** A prior installer exposed only JSON-RPC and routed Nginx to the RPC listener, so the node could be healthy while the browser dashboard was unreachable.

**How to apply:** When changing deployment ports, update both `setup-fullnode-server.sh` and `deploy.sh`; use `--dashboard-port 8080` for an 8080 deployment and open that same TCP port in the host/cloud firewall.

WebSocket subscriptions currently share the JSON-RPC listener at `/api/ws`; `GYDS_WS_PORT` remains only as a compatibility setting and must not be published as an active listener unless a dedicated WebSocket server is implemented.

**Why:** Publishing an unused WebSocket port makes operators believe realtime connections are available there, while wallets and the dashboard already use the RPC origin.

**How to apply:** Keep wallet metadata and guides pointed at `<rpc-origin>/api/ws`, and treat any future standalone WebSocket listener as a coordinated change to the server, workflow, container, firewall, and metadata.