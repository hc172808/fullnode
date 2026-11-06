---
name: Setup configuration safety
description: Boundaries for interactive node and dashboard setup tools.
---

Setup tools should generate or edit protected configuration only. They must not install services, start a second node runtime, restart an existing service, or modify chain data. The dashboard is part of the node process, not a separate service. Testnode is disposable and clears its own `test` data directory on each start, so it must use an isolated path.

**Why:** Production updates must preserve the existing node identity and chain state, and running duplicate runtimes can corrupt or lock node data.

**How to apply:** Keep interactive setup separate from deployment and service management. Use mode-0600 environment files, back up edits, and clearly warn before generating any testnode profile.
