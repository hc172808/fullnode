---
name: Server wallet key setup policy
description: User decision to retain optional server-wallet key import but warn clearly.
---

The user chose to keep private-key and recovery-phrase import in server setup, but add a prominent warning.

**Why:** the user selected the warning-only option rather than removing import.

**How to apply:** preserve the optional import unless the user changes this decision. Keep the warning visible before entry/submission and explain that the key is stored server-side and is not application-encrypted.
