---
name: Consensus engine direction
description: The user's selected direction for GYDS consensus and migration safety.
---

The user selected adopting a mature BFT consensus engine rather than extending
the current custom proposer or targeting Ethereum consensus. The user chose
CometBFT v0.40.0 for an isolated single-validator prototype only; this does not
approve CometBFT as the production engine. A BFT engine does not define GYDS
staking, validator admission, slashing, or economic policy; those remain
application-level decisions.

**Why:** the user chose a mature BFT engine for the P0 consensus work.

**How to apply:** keep the prototype isolated and test its limits explicitly.
Do not change existing testnet or mainnet genesis/history without explicit,
coordinated approval.
