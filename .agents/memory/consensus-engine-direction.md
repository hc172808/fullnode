---
name: Consensus engine direction
description: The user's selected direction for GYDS consensus and migration safety.
---

The user selected adopting a mature BFT consensus engine rather than extending
the current custom proposer or targeting Ethereum consensus, and authorized
preparing a CometBFT v0.40.0 production migration design. This is design-only
authorization, not approval to integrate or activate CometBFT on a live network.
The isolated three-validator test covers empty blocks only. A BFT engine does
not define GYDS staking, validator admission, slashing, or economic policy;
those remain application-level decisions.

**Why:** the user chose a mature BFT engine and specifically asked for a
CometBFT migration design.

**How to apply:** prepare and review the design without switching a live node.
Keep prototypes isolated. Do not change existing testnet or mainnet consensus,
genesis, or history without explicit, coordinated approval.
