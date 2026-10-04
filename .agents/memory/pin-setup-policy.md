---
name: PIN setup policy
description: Where dashboard PINs may be created and how an unset PIN behaves
---

Dashboard PINs are initially created in the setup wizard. After setup, an authenticated Admin session established by the authorized Web3 wallet may replace a forgotten PIN. Public/legacy PIN-setting routes stay disabled. If no PIN exists, the dashboard remains unlocked; if a PIN exists, the dashboard asks for it.

**Why:** The operator chose an Admin-only recovery flow so a forgotten PIN can be replaced without deleting wallet data, while keeping dashboard access protected by the authorized Web3 wallet.

**How to apply:** Preserve setup-time PIN creation and the unlocked path when no PIN is set. PIN recovery/change must require a valid Web3 Admin session, never the old PIN alone, and must not clear wallet data stored in the browser.