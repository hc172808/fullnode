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
	status, message := validateAdminLoginPIN(auth, "192.0.2.10", "2468")
	if status != http.StatusForbidden || !strings.Contains(message, "no dashboard PIN") {
		t.Fatalf("unset PIN result = (%d, %q), want forbidden with setup guidance", status, message)
	}

	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set node PIN: %v", err)
	}
	status, _ = validateAdminLoginPIN(auth, "192.0.2.10", "")
	if status != http.StatusBadRequest {
		t.Fatalf("empty PIN status = %d, want 400", status)
	}
	status, _ = validateAdminLoginPIN(auth, "192.0.2.10", "wrong")
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong PIN status = %d, want 401", status)
	}
	status, message = validateAdminLoginPIN(auth, "192.0.2.10", "2468")
	if status != 0 || message != "" {
		t.Fatalf("correct PIN result = (%d, %q), want success", status, message)
	}
}

func TestValidateAdminLoginPINLocksAfterRepeatedFailures(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	if err := auth.SetPin("2468"); err != nil {
		t.Fatalf("set node PIN: %v", err)
	}
	for attempt := 1; attempt <= maxLoginAttempts; attempt++ {
		status, _ := validateAdminLoginPIN(auth, "192.0.2.20", "wrong")
		if attempt < maxLoginAttempts && status != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, status)
		}
		if attempt == maxLoginAttempts && status != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want 429", attempt, status)
		}
	}
}
