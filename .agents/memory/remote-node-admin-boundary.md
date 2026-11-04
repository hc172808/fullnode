---
name: Remote node administration boundary
description: User's security boundary for controlling other GYDS nodes remotely
---

Do not add arbitrary remote command execution or arbitrary remote file editing
to boostnode administration or other node-management features.

**Why:** The user explicitly identified remote command and file control as a
security risk and said not to implement it.

**How to apply:** Keep remote node-management requests out of scope unless the
user explicitly revisits this boundary with a narrowly defined, safer design.
