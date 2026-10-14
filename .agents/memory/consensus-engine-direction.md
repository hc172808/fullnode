---
name: Consensus engine direction
description: The user's selected direction for GYDS consensus and migration safety.
---

The user selected adopting a mature BFT consensus engine rather than extending
the current custom proposer or targeting Ethereum consensus. CometBFT is a
promising Go-compatible candidate, but the user has not selected that specific
engine. A BFT engine does not define GYDS staking, validator admission, slashing,
or economic policy; those remain application-level decisions.

**Why:** the user chose a mature BFT engine for the P0 consensus work.

**How to apply:** evaluate concrete engine versions against the project's Go
baseline and build experiments on isolated testnet data. Do not change existing
testnet or mainnet genesis/history without explicit, coordinated approval.
