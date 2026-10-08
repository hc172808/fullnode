---
name: Genesis and full-node launch priority
description: The user's stated operational priority for GYDS.
---

The user's immediate priority is to get a genesis node and full nodes started.
The user selected a persistent testnet (chain ID 198281) and separate servers
for the genesis and full nodes. Keep the existing mainnet data untouched.

**Why:** the user explicitly identified this as what they need from the project.

**How to apply:** prioritize genesis/full-node setup, startup, P2P reachability,
and synchronization work. Use the testnet data directory and distinct local
node identities on each server; do not start or reset nodes on the current
mainnet data.
