---
name: Non-root runtime deployment
description: Runtime identity and permissions for native and Docker deployments
---

Native systemd and Docker deployments must run the node as the dedicated `gyds`
account, not root. The account should be a system user with a nologin shell;
Docker management privileges are intentionally separate from node runtime
privileges.

**Why:** Running the blockchain node as root expands the impact of a node or
dashboard compromise, while adding the service account to the Docker group
would grant near-root host access.

**How to apply:** Keep installer-created group/user setup explicit and
deterministic, keep systemd `User`/`Group` set to `gyds`, and declare the same
user in Docker Compose. Ensure data, logs, and generated configuration are
writable/readable by that account without weakening secret permissions.