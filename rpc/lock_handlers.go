package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ── GET /api/lock/status ───────────────────────────────────────────────────
// Returns whether the dashboard PIN has been set by the operator.

func (s *Server) handleLockStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]bool{"pinSet": s.auth.PinIsSet()})
}

// ── POST /api/lock/set ─────────────────────────────────────────────────────
// PINs are created during setup or changed in the Web3-authenticated Admin
// area. This public legacy endpoint remains disabled.

func (s *Server) handleLockSet(w http.ResponseWriter, r *http.Request) {
	jsonErr(w, http.StatusForbidden, "PIN changes require an authenticated Admin session")
}

// ── POST /api/lock/verify ──────────────────────────────────────────────────
// Verify the dashboard PIN. Accepts {"pin":"..."}.
// Shares the three-try IP-based PIN lockout with Admin login, independently of
// the separate rate limit for invalid Web3 signatures.

func (s *Server) handleLockVerify(w http.ResponseWriter, r *http.Request) {
	ip := realIP(r)

	if locked, remaining := s.auth.IsPINLocked(ip); locked {
		mins := int(remaining.Minutes()) + 1
		writePINFailure(w, http.StatusTooManyRequests,
			fmt.Sprintf("Three incorrect PIN attempts. This node is temporarily locked for %d minute(s).", mins),
			s.auth.PinFailureRedirect())
		return
	}

	pin := extractField(r, "pin")
	if pin == "" {
		jsonErr(w, http.StatusBadRequest, "PIN is required")
		return
	}

	if !s.auth.CheckPin(pin) {
		attempts := s.auth.RecordPINFailure(ip)
		s.auth.writeAudit(ip, "LOCK-FAIL")
		if attempts >= maxPINAttempts {
			writePINFailure(w, http.StatusTooManyRequests,
				"Three incorrect PIN attempts. This node is temporarily locked for 15 minutes.",
				s.auth.PinFailureRedirect())
		} else {
			jsonErr(w, http.StatusUnauthorized,
				fmt.Sprintf("Incorrect PIN. %d attempt(s) remain before temporary lockout.", maxPINAttempts-attempts))
		}
		return
	}

	s.auth.ResetPINFailures(ip)
	s.auth.writeAudit(ip, "LOCK-UNLOCK")
	jsonOK(w, map[string]string{"status": "ok"})
}

func writePINFailure(w http.ResponseWriter, status int, message, redirectURL string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":       message,
		"redirectURL": redirectURL,
	})
}
