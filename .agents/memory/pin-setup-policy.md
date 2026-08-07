---
name: PIN setup policy
description: Where dashboard PINs may be created and how an unset PIN behaves
---

Dashboard PIN creation is intentionally limited to the setup wizard. The dashboard must not show a first-visit PIN creation prompt, and alternate admin/API set-PIN routes must not create a PIN. If no PIN exists, the dashboard remains unlocked; if setup created one, the dashboard asks for it.

**Why:** The operator requested PIN setup only while configuring the node, not during normal dashboard access.

**How to apply:** Preserve the setup wizard's PIN field and its server-side apply behavior. When changing dashboard lock UI or auth routes, keep the unset-PIN path open and the existing-PIN verification path protected.