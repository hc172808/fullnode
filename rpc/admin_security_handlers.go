package rpc

import (
	"encoding/json"
	"net/http"
)

type adminPinResetRequest struct {
	PIN        string `json:"pin"`
	ConfirmPIN string `json:"confirmPin"`
}

func validateAdminLoginPIN(auth *AuthStore, ip, pin string) (int, string) {
	if !auth.PinIsSet() {
		auth.writeAudit(ip, "PIN-UNSET")
		return http.StatusForbidden, "This node has no dashboard PIN. Complete setup before Admin login."
	}
	if pin == "" {
		return http.StatusBadRequest, "Enter this node's dashboard PIN."
	}
	if auth.CheckPin(pin) {
		return 0, ""
	}

	attempts := auth.RecordFailure(ip)
	auth.writeAudit(ip, "PIN-FAIL")
	if attempts >= maxLoginAttempts {
		return http.StatusTooManyRequests, "Too many failed attempts. Try again in 15 minutes."
	}
	return http.StatusUnauthorized, "Incorrect PIN."
}

// handleAdminPinReset replaces the dashboard PIN after Web3 admin
// authentication. It deliberately does not require the forgotten PIN.
func (s *Server) handleAdminPinReset(w http.ResponseWriter, r *http.Request) {
	var request adminPinResetRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if request.PIN != request.ConfirmPIN {
		jsonErr(w, http.StatusBadRequest, "PIN entries do not match")
		return
	}
	if err := s.auth.SetPin(request.PIN); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	s.auth.writeAudit(realIP(r), "PIN-RESET")
	jsonOK(w, map[string]string{"message": "Dashboard PIN updated."})
}
