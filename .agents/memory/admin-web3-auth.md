---
name: Admin Web3 authentication
description: The dashboard administrator authenticates by signing a server-issued one-time Ethereum challenge.
---

Admin dashboard access is restricted to the canonical administrator wallet and requires an EVM wallet signature; there is no PIN fallback.

**Why:** The operator explicitly requested Web3-only admin login so access proves control of the administrator wallet without storing or entering a dashboard PIN.

**How to apply:** Preserve one-time challenge expiry and server-side signature recovery when changing login behavior. Never accept a client-provided wallet address without recovering and comparing the signer address.