---
name: Project identity
description: Core identity and nature of this project
---

This project is a **GYDS Chain fullnode** — a Go-based blockchain full node implementation.

- **Type**: Blockchain full node (not a web app, not a UI project)
- **Consensus**: Proof-of-Stake (PoS)
- **API**: Ethereum-compatible JSON-RPC (`eth_`, `net_`, `web3_` methods) + REST API
- **P2P**: TCP-based peer networking on port 30303
- **RPC port**: 5000 (Replit dev), 8545 (production default)
- **Chain ID**: 198282
- **Deployment**: Ubuntu server via `setup-fullnode-server.sh`, installs to `/opt/gyds-fullnode`

**Why:** User explicitly stated "remember this is a fullnode" — context matters when suggesting features, fixes, or architecture changes.

**How to apply:** Frame all suggestions and changes in the context of blockchain node operation, not typical web app patterns.
