---
name: Persistent peer onboarding
description: How operator-added GYDS peers must survive process restarts.
---

An admin-triggered P2P connection is only live state unless its normalized
`host:port` is persisted in `GYDS_BOOTSTRAP_NODES` and a durable
`admin/bootstrap-peers.json` runtime file.

**Why:** The node reconstructs its outbound peer attempts from environment
configuration during startup; a connection made only through the live P2P
server disappears after restart. The file protects the peer list when a
launcher or deployment environment supplies an incomplete `.env`.

**How to apply:** When adding peers through the admin panel or imported node
configuration, deduplicate and persist the address before dialing, then keep
the live peer panel clearly separate from the configured bootstrap list.