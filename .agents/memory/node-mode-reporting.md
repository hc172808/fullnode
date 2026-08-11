---
name: Node mode reporting
description: Deployment and dashboard behavior for GYDS node roles
---

The configured node role must be passed through every runtime surface: the process mode, the dashboard status response, and P2P handshakes. Deployment must explicitly pin the selected role in the installed service environment so a preserved or stale `.env` cannot make a genesis or RPC node appear as full.

**Why:** A hardcoded full-mode handshake and hostname-based dashboard fallback hid the actual role even when startup selected another mode.

**How to apply:** When adding a node mode or changing startup/deployment behavior, update the mode enum, runtime metadata, dashboard label mapping, and installed service environment together.