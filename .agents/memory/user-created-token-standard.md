---
name: User-created token standard
description: Product decision for tokens created by the future token factory
---

User-created tokens should use standard EVM ERC-20 contracts.

**Why:** the user selected standard ERC-20 contracts for user-created assets.

**How to apply:** build the token factory around contract deployment and wallet-signed EVM transactions. Do not implement it using the native GYDS-20 program without asking. Production EVM execution and persistent contract state are prerequisites.