---
name: Setup data directory alignment
description: Dashboard setup must write PIN and node identity into the same data directory the restarted node uses
---

The setup wizard must apply the dashboard PIN to the configured `GYDS_DATA_DIR`, not only to the data directory used by the currently running process. In Replit, the launcher uses `./data` because production paths such as `/var/lib/gyds-fullnode` may not be writable.

**Why:** A PIN stored under the old runtime directory disappears after setup changes the data directory, and an unwritable production path prevents node-key and PIN persistence in the development workspace.

**How to apply:** Keep setup-generated `.env` values shell-safe, ensure the launcher and selected `GYDS_DATA_DIR` agree, and use an absolute service-owned directory for production deployments.