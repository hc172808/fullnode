# Isolated CometBFT consensus prototype

This is a local consensus smoke prototype, separate from the production
fullnode. It uses CometBFT v0.40.0, chain ID `198281`, and a 2,000,000,000
GYDS genesis allocation for testing.

It must not connect to existing GYDS peers or reuse the production `data/`
directory. It does not yet implement signed GYDS transactions, EVM execution,
staking economics, validator updates, or a multi-validator testnet. Do not use
it for funds or production.

Run it from this directory with:

```sh
GOTOOLCHAIN=local go run . --home /tmp/gyds-cometbft-prototype
```

The prototype binds RPC and P2P to loopback by default. Its home contains
CometBFT consensus data and should remain separate from any live chain data.
