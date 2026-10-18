---
name: Consensus engine direction
description: The user's selected direction for GYDS consensus and migration safety.
---

The user selected adopting a mature BFT consensus engine rather than extending
the current custom proposer or targeting Ethereum consensus, and authorized
preparing a CometBFT v0.40.0 production migration design. They also authorized
signed native-transfer validation and deterministic state execution in the
isolated prototype. This does not approve production integration or activation.
A BFT engine does not define GYDS staking, validator admission, slashing, or
economic policy; those remain application-level decisions.

**Why:** the user chose a mature BFT engine and specifically asked for a
CometBFT migration design and an isolated transaction-execution prototype. The
prototype previously shared the persistent testnet chain ID; once signed
transactions were introduced, that created a cross-network replay risk.

**How to apply:** keep signed-transaction work inside the isolated prototype and
keep its EIP-155 chain ID distinct from every configured network. Do not change
existing testnet or mainnet consensus, genesis, or history without explicit,
coordinated approval.
