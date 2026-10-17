---
name: Consensus engine direction
description: The user's selected direction for GYDS consensus and migration safety.
---

The user selected adopting a mature BFT consensus engine rather than extending
the current custom proposer or targeting Ethereum consensus. CometBFT v0.40.0
has been used only in isolated prototype work, including a three-validator
empty-block smoke test; this does not approve it as the production engine. A
BFT engine does not define GYDS staking, validator admission, slashing, or
economic policy; those remain application-level decisions.

**Why:** the user chose a mature BFT engine for the P0 consensus work.

**How to apply:** keep prototypes isolated and test their limits explicitly.
Do not change existing testnet or mainnet consensus, genesis, or history without
explicit, coordinated approval.
