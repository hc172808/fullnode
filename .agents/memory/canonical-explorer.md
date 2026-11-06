---
name: Canonical explorer
description: Permanent public explorer URL for GYDS wallet and node metadata
---

The canonical GYDS block explorer is `https://explorer.netlifegy.com` for every
node and wallet network configuration.

The default mainnet wallet RPC is `https://app.netlifegy.com/api/rpc?network=mainnet`.
Operators may override the wallet-facing endpoint with `GYDS_PUBLIC_RPC_URL` in
Admin Node Control or `.env`. Preserve the full path and query string.

**Why:** The explorer is a shared public service and must not change based on
which node serves the wallet metadata. The mainnet RPC is a remote service whose
gateway path may include a network query parameter, so node operators need to
change the exact advertised endpoint without confusing it with their local RPC
listener or dashboard origin.

**How to apply:** Keep wallet `blockExplorerUrls`, network `explorerUrls`, and
downloadable connection information pointed to this URL.
Advertise the configured public RPC URL in wallet/network metadata; do not
append paths to it or drop its query parameters.