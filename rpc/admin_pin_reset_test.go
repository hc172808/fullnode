package rpc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminPinResetRequiresAdminSessionAndReplacesPin(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set initial PIN: %v", err)
	}
	s := &Server{auth: auth}
	handler := s.requireAdminSession(s.handleAdminPinReset)
	body := `{"pin":"N3w-PIN","confirmPin":"N3w-PIN"}`

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest("POST", "/admin/security/pin", strings.NewReader(body)))
	if unauthenticated.Code != 401 {
		t.Fatalf("unauthenticated status = %d, want 401", unauthenticated.Code)
	}
	if !auth.CheckPin("2468") {
		t.Fatal("unauthenticated request changed the existing PIN")
	}

	request := httptest.NewRequest("POST", "/admin/security/pin", strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.NewSession()})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("authenticated status = %d, body = %s", response.Code, response.Body.String())
	}
	if !auth.CheckPin("N3w-PIN") {
		t.Fatal("new PIN was not saved")
	}
	if auth.CheckPin("2468") {
		t.Fatal("old PIN still works after reset")
	}
	audit, err := os.ReadFile(filepath.Join(auth.dataDir, "admin", "access.log"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(audit), "PIN-RESET") {
		t.Fatalf("PIN reset missing from audit log: %s", audit)
	}
}

func TestAdminPinResetRejectsMismatchWithoutChangingPIN(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set initial PIN: %v", err)
	}
	s := &Server{auth: auth}
	request := httptest.NewRequest(
		"POST",
		"/admin/security/pin",
		strings.NewReader(`{"pin":"N3w-PIN","confirmPin":"different"}`),
	)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.NewSession()})
	response := httptest.NewRecorder()
	s.requireAdminSession(s.handleAdminPinReset).ServeHTTP(response, request)

	if response.Code != 400 {
		t.Fatalf("mismatch status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
	if !auth.CheckPin("2468") {
		t.Fatal("mismatched PIN entries changed the existing PIN")
	}
}

func TestValidateAdminLoginPINRequiresThisNodesConfiguredPIN(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	status, message, _ := validateAdminLoginPIN(auth, "192.0.2.10", "2468")
	if status != http.StatusForbidden || !strings.Contains(message, "no dashboard PIN") {
		t.Fatalf("unset PIN result = (%d, %q), want forbidden with setup guidance", status, message)
	}

	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set node PIN: %v", err)
	}
	status, _, _ = validateAdminLoginPIN(auth, "192.0.2.10", "")
	if status != http.StatusBadRequest {
		t.Fatalf("empty PIN status = %d, want 400", status)
	}
	status, _, _ = validateAdminLoginPIN(auth, "192.0.2.10", "wrong")
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong PIN status = %d, want 401", status)
	}
	status, message, _ = validateAdminLoginPIN(auth, "192.0.2.10", "2468")
	if status != 0 || message != "" {
		t.Fatalf("correct PIN result = (%d, %q), want success", status, message)
	}
}

func TestValidateAdminLoginPINLocksAfterRepeatedFailures(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set node PIN: %v", err)
	}
	if err := auth.SetPinFailureRedirect("https://example.org/blocked"); err != nil {
		t.Fatalf("set PIN redirect: %v", err)
	}
	for attempt := 1; attempt <= maxPINAttempts; attempt++ {
		status, _, redirectURL := validateAdminLoginPIN(auth, "192.0.2.20", "wrong")
		if attempt < maxPINAttempts && status != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, status)
		}
		if attempt == maxPINAttempts {
			if status != http.StatusTooManyRequests {
				t.Fatalf("attempt %d status = %d, want 429", attempt, status)
			}
			if redirectURL != "https://example.org/blocked" {
				t.Fatalf("redirect = %q, want configured URL", redirectURL)
			}
		}
	}
}

func TestPINFailureRedirectValidationAndPersistence(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	for _, target := range []string{
		"https://example.org/blocked",
		"/access-help",
		"",
	} {
		if err := auth.SetPinFailureRedirect(target); err != nil {
			t.Fatalf("SetPinFailureRedirect(%q): %v", target, err)
		}
		if got := auth.PinFailureRedirect(); got != target {
			t.Fatalf("redirect = %q, want %q", got, target)
		}
	}
	for _, target := range []string{
		"http://example.org/blocked",
		"//example.org/blocked",
		"/\\example.org/blocked",
		"https://user:pass@example.org/",
		"javascript:alert(1)",
	} {
		if err := auth.SetPinFailureRedirect(target); err == nil {
			t.Fatalf("SetPinFailureRedirect(%q) succeeded; want rejection", target)
		}
	}
	if err := auth.SetPinFailureRedirect("https://example.org/after-lockout"); err != nil {
		t.Fatalf("set persisted redirect: %v", err)
	}
	reopened := NewAuthStore(auth.dataDir)
	if got := reopened.PinFailureRedirect(); got != "https://example.org/after-lockout" {
		t.Fatalf("redirect after reopening AuthStore = %q", got)
	}
}

func TestPINRedirectSettingsRequireAdminSessionAndPersist(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	s := &Server{auth: auth}
	handler := s.requireAdminSession(s.handleAdminPINRedirectSettings)

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest("GET", "/admin/security/pin/redirect", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated settings status = %d, want 401", unauthenticated.Code)
	}

	request := httptest.NewRequest("POST", "/admin/security/pin/redirect", strings.NewReader(`{"redirectURL":"https://example.org/locked"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.NewSession()})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated settings status = %d; body=%s", response.Code, response.Body.String())
	}
	if got := NewAuthStore(auth.dataDir).PinFailureRedirect(); got != "https://example.org/locked" {
		t.Fatalf("saved redirect = %q", got)
	}
}

func TestPINRecoveryChallengeIsScopedAndOneTime(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	nonce, message := auth.NewPINRecoveryChallenge()
	if !strings.HasPrefix(message, pinRecoveryChallengeTitle+"\n") {
		t.Fatalf("recovery challenge has unexpected purpose: %q", message)
	}
	if _, ok := auth.ConsumeChallenge(nonce); !ok {
		t.Fatal("fresh PIN recovery challenge was not consumable")
	}
	if _, ok := auth.ConsumeChallenge(nonce); ok {
		t.Fatal("PIN recovery challenge could be consumed twice")
	}
}

func TestDashboardPINCanBeRotatedFromEnvironment(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set initial PIN: %v", err)
	}
	t.Setenv("GYDS_DASHBOARD_PIN", "Env-New-PIN")
	if err := auth.ApplyPinFromEnvironment(); err != nil {
		t.Fatalf("apply environment PIN: %v", err)
	}
	if !auth.CheckPin("Env-New-PIN") {
		t.Fatal("environment PIN did not replace the stored PIN")
	}
	if auth.CheckPin("2468") {
		t.Fatal("previous PIN still works after environment rotation")
	}
}

func TestDashboardPINPersistsAcrossAuthStoreRestart(t *testing.T) {
	dataDir := t.TempDir()
	if err := NewAuthStore(dataDir).SetPin("Persisted-PIN"); err != nil {
		t.Fatalf("set PIN: %v", err)
	}
	if !NewAuthStore(dataDir).CheckPin("Persisted-PIN") {
		t.Fatal("PIN was not available after reopening AuthStore")
	}
}

func TestPINRecoveryRejectsOrdinaryLoginChallenge(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set initial PIN: %v", err)
	}
	nonce, _ := auth.NewChallenge()
	s := &Server{auth: auth}
	body := `{"address":"` + AdminWallet + `","nonce":"` + nonce + `","signature":"0x","pin":"New-PIN","confirmPin":"New-PIN"}`
	request := httptest.NewRequest("POST", "/admin/security/pin/recover", strings.NewReader(body))
	response := httptest.NewRecorder()
	s.handleAdminPINRecovery(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary login challenge status = %d, want 401; body=%s", response.Code, response.Body.String())
	}
	if !auth.CheckPin("2468") {
		t.Fatal("rejected recovery request changed the existing PIN")
	}
}

func TestLockVerifyReturnsConfiguredRedirectOnThirdIncorrectPIN(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set PIN: %v", err)
	}
	if err := auth.SetPinFailureRedirect("/blocked"); err != nil {
		t.Fatalf("set redirect: %v", err)
	}
	s := &Server{auth: auth}
	for attempt := 1; attempt <= maxPINAttempts; attempt++ {
		request := httptest.NewRequest("POST", "/api/lock/verify", strings.NewReader(`{"pin":"wrong"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.handleLockVerify(response, request)
		if attempt < maxPINAttempts && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, response.Code)
		}
		if attempt == maxPINAttempts {
			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("third attempt status = %d, want 429", response.Code)
			}
			if !strings.Contains(response.Body.String(), `"redirectURL":"/blocked"`) {
				t.Fatalf("third-attempt response has no redirect URL: %s", response.Body.String())
			}
		}
	}
}
