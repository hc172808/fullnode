package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const pinRecoveryChallengeTitle = "GYDS Chain Admin PIN Recovery"

type adminPinResetRequest struct {
	PIN        string `json:"pin"`
	ConfirmPIN string `json:"confirmPin"`
}

func validateAdminLoginPIN(auth *AuthStore, ip, pin string) (int, string, string) {
	if !auth.PinIsSet() {
		auth.writeAudit(ip, "PIN-UNSET")
		return http.StatusForbidden, "This node has no dashboard PIN. Complete setup before Admin login.", ""
	}
	if pin == "" {
		return http.StatusBadRequest, "Enter this node's dashboard PIN.", ""
	}
	if locked, remaining := auth.IsPINLocked(ip); locked {
		mins := int(remaining.Minutes()) + 1
		return http.StatusTooManyRequests,
			fmt.Sprintf("Three incorrect PIN attempts. This node is temporarily locked for %d minute(s).", mins),
			auth.PinFailureRedirect()
	}
	if auth.CheckPin(pin) {
		auth.ResetPINFailures(ip)
		return 0, "", ""
	}

	attempts := auth.RecordPINFailure(ip)
	auth.writeAudit(ip, "PIN-FAIL")
	if attempts >= maxPINAttempts {
		return http.StatusTooManyRequests, "Three incorrect PIN attempts. This node is temporarily locked for 15 minutes.",
			auth.PinFailureRedirect()
	}
	return http.StatusUnauthorized, fmt.Sprintf("Incorrect PIN. %d attempt(s) remain before temporary lockout.", maxPINAttempts-attempts), ""
}

// handleAdminPINRecoveryChallenge issues a one-time Web3 challenge whose
// signature authorizes only recovery of this node's PIN.
func (s *Server) handleAdminPINRecoveryChallenge(w http.ResponseWriter, r *http.Request) {
	nonce, message := s.auth.NewPINRecoveryChallenge()
	jsonOK(w, map[string]string{"nonce": nonce, "message": message, "address": AdminWallet})
}

// handleAdminPINRecovery lets the configured Admin wallet replace a forgotten
// PIN without granting an Admin session or access to any other Admin route.
func (s *Server) handleAdminPINRecovery(w http.ResponseWriter, r *http.Request) {
	ip := realIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if locked, remaining := s.auth.IsLocked(ip); locked {
		mins := int(remaining.Minutes()) + 1
		jsonErr(w, http.StatusTooManyRequests, fmt.Sprintf("Too many invalid Admin signatures. Try again in %d minute(s).", mins))
		return
	}

	var request struct {
		Address    string `json:"address"`
		Nonce      string `json:"nonce"`
		Signature  string `json:"signature"`
		PIN        string `json:"pin"`
		ConfirmPIN string `json:"confirmPin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if request.PIN != request.ConfirmPIN {
		jsonErr(w, http.StatusBadRequest, "PIN entries do not match")
		return
	}
	if strings.ToLower(strings.TrimSpace(request.Address)) != strings.ToLower(AdminWallet) {
		s.auth.RecordFailure(ip)
		s.auth.writeAudit(ip, "PIN-RECOVERY-FAIL")
		jsonErr(w, http.StatusUnauthorized, "Connected wallet is not the authorized Admin wallet")
		return
	}
	message, ok := s.auth.ConsumeChallenge(strings.TrimSpace(request.Nonce))
	if !ok || !strings.HasPrefix(message, pinRecoveryChallengeTitle+"\n") {
		s.auth.RecordFailure(ip)
		s.auth.writeAudit(ip, "PIN-RECOVERY-FAIL")
		jsonErr(w, http.StatusUnauthorized, "PIN recovery challenge is invalid or expired. Request a new one.")
		return
	}
	recovered, err := recoverEthereumAddress(message, request.Signature)
	if err != nil || !strings.EqualFold(recovered, AdminWallet) {
		s.auth.RecordFailure(ip)
		s.auth.writeAudit(ip, "PIN-RECOVERY-FAIL")
		jsonErr(w, http.StatusUnauthorized, "Signature did not verify as the authorized Admin wallet")
		return
	}
	if err := s.auth.SetPin(request.PIN); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.ResetFailures(ip)
	s.auth.ResetPINFailures(ip)
	s.auth.writeAudit(ip, "PIN-RECOVER")
	jsonOK(w, map[string]string{"message": "Dashboard PIN replaced. Sign in using the new PIN."})
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

func (s *Server) handleAdminPINRedirectSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		jsonOK(w, map[string]string{"redirectURL": s.auth.PinFailureRedirect()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var request struct {
		RedirectURL string `json:"redirectURL"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.auth.SetPinFailureRedirect(request.RedirectURL); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.writeAudit(realIP(r), "PIN-REDIRECT-SET")
	jsonOK(w, map[string]string{"message": "PIN failure redirect saved.", "redirectURL": s.auth.PinFailureRedirect()})
}
