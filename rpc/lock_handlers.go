package rpc

import (
	"fmt"
	"net/http"
)

// ── GET /api/lock/status ───────────────────────────────────────────────────
// Returns whether the dashboard PIN has been set by the operator.

func (s *Server) handleLockStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]bool{"pinSet": s.auth.PinIsSet()})
}

// ── POST /api/lock/set ─────────────────────────────────────────────────────
// First-time PIN setup. Accepts {"pin":"...","confirm":"..."}.
// Fails if a PIN is already stored.

func (s *Server) handleLockSet(w http.ResponseWriter, r *http.Request) {
	if s.auth.PinIsSet() {
		jsonErr(w, http.StatusForbidden, "PIN is already set")
		return
	}
	fields := extractFields(r, "pin", "confirm")
	pin, confirm := fields["pin"], fields["confirm"]
	if pin == "" {
		jsonErr(w, http.StatusBadRequest, "PIN is required")
		return
	}
	if pin != confirm {
		jsonErr(w, http.StatusBadRequest, "PINs do not match")
		return
	}
	if err := s.auth.SetPin(pin); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.writeAudit(realIP(r), "LOCK-PIN-SET")
	jsonOK(w, map[string]string{"status": "ok"})
}

// ── POST /api/lock/verify ──────────────────────────────────────────────────
// Verify the dashboard PIN. Accepts {"pin":"..."}.
// Shares the same IP-based rate-limit / lockout as the admin login.

func (s *Server) handleLockVerify(w http.ResponseWriter, r *http.Request) {
	ip := realIP(r)

	if locked, remaining := s.auth.IsLocked(ip); locked {
		mins := int(remaining.Minutes()) + 1
		jsonErr(w, http.StatusTooManyRequests,
			fmt.Sprintf("Too many failed attempts. Try again in %d minute(s).", mins))
		return
	}

	pin := extractField(r, "pin")
	if pin == "" {
		jsonErr(w, http.StatusBadRequest, "PIN is required")
		return
	}

	if !s.auth.CheckPin(pin) {
		attempts := s.auth.RecordFailure(ip)
		left := maxLoginAttempts - attempts
		s.auth.writeAudit(ip, "LOCK-FAIL")
		if left <= 0 {
			jsonErr(w, http.StatusUnauthorized,
				"Incorrect PIN. Too many failures — wait 15 minutes.")
		} else {
			jsonErr(w, http.StatusUnauthorized,
				fmt.Sprintf("Incorrect PIN. %d attempt(s) remaining.", left))
		}
		return
	}

	s.auth.ResetFailures(ip)
	s.auth.writeAudit(ip, "LOCK-UNLOCK")
	jsonOK(w, map[string]string{"status": "ok"})
}
