---
name: Persistent peer onboarding
description: How operator-added GYDS peers must survive process restarts.
---

An admin-triggered P2P connection is only live state unless its normalized
`host:port` is also persisted in `GYDS_BOOTSTRAP_NODES`.

**Why:** The node reconstructs its outbound peer attempts from environment
configuration during startup; a connection made only through the live P2P
server disappears after restart.

**How to apply:** When adding peers through the admin panel or imported node
configuration, deduplicate and persist the address, then keep the live peer
panel clearly separate from the configured bootstrap list.