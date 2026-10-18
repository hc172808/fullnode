# Isolated CometBFT consensus prototype

This is an isolated consensus and state-execution prototype, separate from the
production fullnode. It uses CometBFT v0.40.0, test-only chain ID `9198281`, and
a 2,000,000,000 GYDS genesis allocation.

It must not connect to existing GYDS peers or reuse the production `data/`
directory. It accepts only canonical, chain-ID-protected EIP-155 legacy
transactions that transfer native GYDS to an account. The prototype requires a
21,000 gas limit and zero gas price, enforces balance and nonce rules, and
commits deterministic state hashes. These are test semantics, not approved
production fee or transaction rules.

Mempool admission checks against committed state only; a future-nonce
transaction from an account is not admitted until its prior transaction is
committed. Production requires an explicit pending-nonce, fee, and mempool
policy.

The test suite exercises signing, chain-ID/signature/nonce validation, transfer
execution, persistence/restart, and a temporary three-validator cluster that
commits a signed transfer with matching block and app hashes. It does not
implement EVM execution, contract calls, staking economics, validator updates,
production transaction fees, or an operator-facing testnet. Do not use it for
funds or production.

Run it from this directory with:

```sh
GOTOOLCHAIN=local go run . --home /tmp/gyds-cometbft-prototype-9198281
```

The prototype binds RPC and P2P to loopback by default. Its home contains
CometBFT consensus data and should remain separate from any live chain data.
