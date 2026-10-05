---
name: P2P genesis compatibility
description: Genesis-hash requirements for accepting GYDS peer handshakes
---

When a node has a configured genesis hash, its peers must send the same hash
during the handshake. A peer that omits the hash is not verifiable and must be
rejected; this also means nodes using the older handshake are incompatible.

**Why:** Chain ID alone cannot distinguish incompatible chains. Accepting a
peer without a verifiable genesis identity risks connecting across a fork.

**How to apply:** Keep the genesis hash in the handshake contract. If the P2P
protocol changes, coordinate upgrades so peers are not silently accepted without
genesis verification.
