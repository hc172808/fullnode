---
name: GYDS supply targets
description: User-requested native GYDS genesis supply for mainnet and persistent testnet.
---

The user requested initial native GYDS supplies of 3,000,000,000 GYDS on
mainnet and 2,000,000,000 GYDS on persistent testnet chain ID 198281. This is a
target for a coordinated genesis/migration, not permission to reset existing
chain data. The current code still specifies 1,000,000,000 GYDS for both
canonical profiles; disposable chain ID 31337 is separate.

**Why:** the user explicitly stated these supply targets.

**How to apply:** preserve existing mainnet and testnet data until migration is
coordinated. Before changing allocations, confirm whether to retain the current
50/30/20 split or use new recipient addresses and amounts.
