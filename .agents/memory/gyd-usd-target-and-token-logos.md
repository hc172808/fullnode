---
name: GYD USD target and token logos
description: Product requirements for the GYD stablecoin target and wallet-facing logo metadata
---

GYD's intended target is 1 GYD = 1 USD, not 1 GYD = 1 Guyana Dollar. Configure its published USD target through `GYD_USD_TARGET` in `.env`; this is informational metadata only and does not enforce a price, reserves, or redemption. Publish a stable, public HTTPS URL for the GYD token logo that is distinct from the GYDS network logo. Wallet display is wallet-specific and cannot be guaranteed in every wallet.

The USD peg is a product target, not evidence of working reserves, redemption, or price stability. GYD is currently node-managed and not yet a standard ERC-20 contract. Do not claim it is backed or redeemable until the USD reserve/redemption model exists and is independently verified.

The operator selected mainnet chain ID 198282 for readiness work only, chose a one-to-one migration of the existing 10B GYD balances into a standard ERC-20, and selected a multisig treasury for mint authority. These choices do not authorize mainnet EVM activation or deployment; consensus, protocol-migration, multi-node, live-balance-snapshot, and multisig-verification gates must pass first.

**Why:** the user explicitly clarified the intended USD target, asked for the target value to live in `.env`, and requested a URL-based logo usable by external wallets.

**How to apply:** use USD in token contracts, metadata, copy, and reserve/redemption planning. Read `GYD_USD_TARGET` for the displayed metadata value, but never describe configuration as proof of a working peg. Give the GYD token its own public HTTPS logo URL; keep network and token logos separate. Integrate metadata with each target wallet's registry and test actual display. For mainnet GYD work, preserve chain data, do not activate or deploy until all safety gates pass, migrate rather than duplicate the 10B supply, and use only a verified multisig treasury address—never collect its keys.
