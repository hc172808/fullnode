---
name: Node mode reporting
description: Deployment and dashboard behavior for GYDS node roles
---

The configured node role must be passed through every runtime surface: the process mode, the dashboard status response, and P2P handshakes. Deployment must explicitly pin the selected role in the installed service environment so a preserved or stale `.env` cannot make a genesis or RPC node appear as full.

**Why:** A hardcoded full-mode handshake and hostname-based dashboard fallback hid the actual role even when startup selected another mode.

**How to apply:** When adding a node mode or changing startup/deployment behavior, update the mode enum, runtime metadata, dashboard label mapping, and installed service environment together.

Genesis nodes are the local source of their chain and must be ready to serve their locally produced chain without an authorized peer connection. Peer synchronization remains required for roles that join an existing chain.

**Why:** A genesis node with a valid local chain was blocked from serving wallet/RPC requests when it had no peers, causing clients to report the network as busy even though the node was producing blocks.

**How to apply:** Keep readiness checks role-aware: do not require peer-height handshakes for genesis mode, but retain them for nodes that must catch up from peers.