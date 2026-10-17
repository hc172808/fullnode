---
name: Production launch target
description: The user's current mainnet deployment target and data-preservation expectations.
---

The user selected the existing mainnet as the production target on their own
server; Replit is for development/preview, not the only deployment target.
Routine Git updates must preserve chain data, node identity, and operator
configuration. A fresh-data reset must be a separate, explicit, confirmed
operation and retain an archive of the previous data.

**Why:** the user explicitly chose the existing mainnet and their own server,
and asked to update from Git while being able to choose whether to reset data.

**How to apply:** prioritize the server install and safe Git update path. Do not
change or reset existing mainnet history/genesis during ordinary deployment.
Keep a reset opt-in and restorable. Do not describe the chain as production
ready until the remaining consensus, migration, peer, security, and independent
review gates have passed.
