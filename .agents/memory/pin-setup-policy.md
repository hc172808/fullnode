---
name: PIN setup policy
description: Where dashboard PINs may be created and how an unset PIN behaves
---

Dashboard PINs are initially created in the setup wizard. After setup, an authenticated Admin session may change the PIN; a separate one-time Web3 signature from the authorized wallet may recover a forgotten PIN without creating an Admin session. A non-empty `GYDS_DASHBOARD_PIN` overrides the stored hash on every startup, so remove or update it after changing the PIN in the Admin UI. After three incorrect PIN attempts from one IP, lock PIN checks and redirect to the Admin-configured HTTPS URL or same-site path. Public/legacy PIN-setting routes stay disabled. If no PIN exists, the dashboard remains unlocked; if a PIN exists, the dashboard asks for it.

**Why:** The user requires PIN changes through Web3 or `.env`, recovery when login is blocked by a forgotten PIN, and an Admin-configurable destination after three wrong attempts.

**How to apply:** Preserve setup-time PIN creation and the unlocked path when no PIN is set. Use a fresh, one-time Admin-wallet proof for recovery, do not create a general Admin session from recovery, never return/log plaintext PINs, and keep `.env` precedence clear to operators.