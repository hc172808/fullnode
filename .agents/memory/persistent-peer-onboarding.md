---
name: Persistent peer onboarding
description: How operator-added GYDS peers must survive process restarts.
---

Peer additions and removals from admin controls and imported node files are
pending changes until the operator clicks Apply. Staged imports are held in
server memory; they are intentionally lost if the process restarts before Apply.
Apply writes the submitted peer list to both `GYDS_BOOTSTRAP_NODES` and the
durable peer file, replacing the prior list rather than appending to it.

**Why:** The operator expects Apply to be the commit point. Earlier add/remove
paths persisted immediately, so changes could take effect before Apply and
could not be reliably reviewed or discarded.

**How to apply:** Route peer edits into pending state or the form only. Validate
and replace both durable peer sources only in the Apply handler, then restart
the node so the saved list drives outbound connections.