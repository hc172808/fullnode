---
name: Admin Web3 authentication
description: The dashboard administrator authenticates by signing a server-issued one-time Ethereum challenge.
---

Admin dashboard sessions require both a signature over a one-time challenge from the canonical administrator wallet and this node's dashboard PIN. Neither the wallet signature nor the PIN alone grants a session. PIN recovery requires a fresh admin-wallet signature and does not create an admin session.

Before requesting a login signature, the browser must compare the connected wallet with the server-authorized address and recheck the active account immediately before `personal_sign`. A wrong or changed wallet must be rejected with a clear diagnostic and no signature prompt. The server independently verifies the submitted address and recovers the actual signer.

**Why:** The operator wants the wrong wallet rejected before signing and clear diagnostics for the Web3-plus-PIN login flow.

**How to apply:** Preserve one-time challenge expiry, the per-node PIN, client-side wallet preflight, and server-side signature recovery. Never trust a client-provided wallet address without recovering and comparing the signer address.