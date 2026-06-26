package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ── GET /admin/login ──────────────────────────────────────────────────────────

func (s *Server) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.auth.PinIsSet() {
		http.Redirect(w, r, "/admin/set-pin", http.StatusFound)
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
	jsonOK(w, map[string]string{"status": "ok", "redirect": "/"})
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
	if s.auth.PinIsSet() {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	serveStaticPage(w, "static/admin-set-pin.html")
}

// ── POST /admin/set-pin ───────────────────────────────────────────────────────

func (s *Server) handleAdminSetPinSubmit(w http.ResponseWriter, r *http.Request) {
	if s.auth.PinIsSet() {
		jsonErr(w, http.StatusForbidden, "PIN is already set and cannot be changed here")
		return
	}
	pin := extractField(r, "pin")
	confirm := extractField(r, "confirm")
	if pin != confirm {
		jsonErr(w, http.StatusBadRequest, "PINs do not match")
		return
	}
	if err := s.auth.SetPin(pin); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.writeAudit(realIP(r), "PIN-SET")
	jsonOK(w, map[string]string{"status": "ok", "redirect": "/admin/login"})
}

// ── GET /admin/wallet ─────────────────────────────────────────────────────────
// Returns the hardcoded admin wallet so the login page can display it.

func (s *Server) handleAdminWallet(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"adminWallet": AdminWallet})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// extractField reads a named field from either a JSON body or a form POST.
func extractField(r *http.Request, field string) string {
	ct := r.Header.Get("Content-Type")
	if ct == "application/json" || ct == "application/json; charset=utf-8" {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			return ""
		}
		var m map[string]string
		if err := json.Unmarshal(body, &m); err != nil {
			return ""
		}
		return m[field]
	}
	_ = r.ParseForm()
	return r.FormValue(field)
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
