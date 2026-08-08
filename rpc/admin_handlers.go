package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ── GET /admin/login ──────────────────────────────────────────────────────────

func (s *Server) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.auth.PinIsSet() {
		http.Redirect(w, r, "/setup?step=6", http.StatusFound)
		return
	}
	serveStaticPage(w, "static/admin-login.html")
}

// ── POST /admin/login ─────────────────────────────────────────────────────────

func (s *Server) handleAdminLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ip := realIP(r)

	if locked, remaining := s.auth.IsLocked(ip); locked {
		mins := int(remaining.Minutes()) + 1
		s.auth.writeAudit(ip, "BLOCKED")
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
		s.auth.writeAudit(ip, "FAIL")
		if left <= 0 {
			jsonErr(w, http.StatusUnauthorized,
				fmt.Sprintf("Incorrect PIN. IP locked for %d minutes.", int(lockoutDuration.Minutes())))
		} else {
			jsonErr(w, http.StatusUnauthorized,
				fmt.Sprintf("Incorrect PIN. %d attempt(s) remaining before lockout.", left))
		}
		return
	}

	s.auth.ResetFailures(ip)
	token := s.auth.NewSession()
	s.auth.writeAudit(ip, "LOGIN")

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	jsonOK(w, map[string]string{"status": "ok", "redirect": "/admin/db"})
}

// ── GET /admin/logout ─────────────────────────────────────────────────────────

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.auth.Logout(cookie.Value)
		s.auth.writeAudit(realIP(r), "LOGOUT")
	}
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookieName,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// ── GET /admin/set-pin ────────────────────────────────────────────────────────

func (s *Server) handleAdminSetPinPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/setup?step=6", http.StatusFound)
}

// ── POST /admin/set-pin ───────────────────────────────────────────────────────

func (s *Server) handleAdminSetPinSubmit(w http.ResponseWriter, r *http.Request) {
	jsonErr(w, http.StatusForbidden, "PIN can only be set during the setup wizard")
}

// ── GET /admin/wallet ─────────────────────────────────────────────────────────
// Returns the hardcoded admin wallet so the login page can display it.

func (s *Server) handleAdminWallet(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"adminWallet": AdminWallet})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// extractFields reads all named fields from a JSON body or form POST in one
// pass. The body is consumed only once, so multiple fields work correctly.
func extractFields(r *http.Request, fields ...string) map[string]string {
	result := make(map[string]string, len(fields))
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			return result
		}
		var m map[string]string
		if err := json.Unmarshal(body, &m); err != nil {
			return result
		}
		for _, f := range fields {
			result[f] = m[f]
		}
		return result
	}
	_ = r.ParseForm()
	for _, f := range fields {
		result[f] = r.FormValue(f)
	}
	return result
}

// extractField reads a single field — convenience wrapper around extractFields.
func extractField(r *http.Request, field string) string {
	return extractFields(r, field)[field]
}

func serveStaticPage(w http.ResponseWriter, path string) {
	data, err := staticFiles.ReadFile(path)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
