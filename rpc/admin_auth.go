package rpc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AdminWallet is the sole authorised administrator address.
// This value is burned into the binary and MUST NOT be changed at runtime.
const AdminWallet = "0x6422d12bfaddee5142bfad21b3006a74d09017b1"

const (
	sessionCookieName = "gyds_admin_session"
	sessionTTL        = 8 * time.Hour
	maxLoginAttempts  = 5
	lockoutDuration   = 15 * time.Minute
	pinMinLen         = 4
	pinMaxLen         = 16
)

// ── Session ───────────────────────────────────────────────────────────────────

type adminSession struct {
	token    string
	created  time.Time
	lastSeen time.Time
}

// ── IP rate-limit record ──────────────────────────────────────────────────────

type ipRecord struct {
	attempts int
	lockedAt time.Time
}

// ── AuthStore ─────────────────────────────────────────────────────────────────

type AuthStore struct {
	mu       sync.Mutex
	sessions map[string]*adminSession
	ipMap    map[string]*ipRecord
	dataDir  string
}

func NewAuthStore(dataDir string) *AuthStore {
	return &AuthStore{
		sessions: make(map[string]*adminSession),
		ipMap:    make(map[string]*ipRecord),
		dataDir:  dataDir,
	}
}

// ── PIN storage ───────────────────────────────────────────────────────────────

func (a *AuthStore) pinFile() string {
	return filepath.Join(a.dataDir, "admin", ".pin_hash")
}

func (a *AuthStore) auditLog() string {
	return filepath.Join(a.dataDir, "admin", "access.log")
}

func (a *AuthStore) PinIsSet() bool {
	_, err := os.Stat(a.pinFile())
	return err == nil
}

func (a *AuthStore) SetPin(raw string) error {
	if len(raw) < pinMinLen || len(raw) > pinMaxLen {
		return fmt.Errorf("PIN must be %d–%d characters", pinMinLen, pinMaxLen)
	}
	hash := hashPin(raw)
	dir := filepath.Dir(a.pinFile())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(a.pinFile(), []byte(hash), 0600)
}

func (a *AuthStore) CheckPin(raw string) bool {
	data, err := os.ReadFile(a.pinFile())
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == hashPin(raw)
}

func hashPin(raw string) string {
	sum := sha256.Sum256([]byte("gyds-admin-pin:" + raw))
	return hex.EncodeToString(sum[:])
}

// ── Audit log ─────────────────────────────────────────────────────────────────

func (a *AuthStore) writeAudit(ip, event string) {
	line := fmt.Sprintf("%s  %-7s  ip=%s  wallet=%s\n",
		time.Now().UTC().Format("2006-01-02T15:04:05Z"), event, ip, AdminWallet)
	_ = os.MkdirAll(filepath.Dir(a.auditLog()), 0700)
	f, err := os.OpenFile(a.auditLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// ── IP locking ────────────────────────────────────────────────────────────────

func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		addr = addr[:idx]
	}
	return strings.Trim(addr, "[]")
}

func (a *AuthStore) IsLocked(ip string) (bool, time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.ipMap[ip]
	if !ok {
		return false, 0
	}
	if rec.attempts >= maxLoginAttempts {
		remaining := lockoutDuration - time.Since(rec.lockedAt)
		if remaining > 0 {
			return true, remaining
		}
		delete(a.ipMap, ip)
	}
	return false, 0
}

func (a *AuthStore) RecordFailure(ip string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.ipMap[ip]
	if !ok {
		rec = &ipRecord{}
		a.ipMap[ip] = rec
	}
	rec.attempts++
	if rec.attempts >= maxLoginAttempts {
		rec.lockedAt = time.Now()
	}
	return rec.attempts
}

func (a *AuthStore) ResetFailures(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.ipMap, ip)
}

func (a *AuthStore) AttemptsLeft(ip string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.ipMap[ip]
	if !ok {
		return maxLoginAttempts
	}
	left := maxLoginAttempts - rec.attempts
	if left < 0 {
		left = 0
	}
	return left
}

// ── Session management ────────────────────────────────────────────────────────

func (a *AuthStore) NewSession() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions = make(map[string]*adminSession)
	a.sessions[token] = &adminSession{
		token:    token,
		created:  time.Now(),
		lastSeen: time.Now(),
	}
	return token
}

func (a *AuthStore) ValidSession(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[token]
	if !ok {
		return false
	}
	if time.Since(s.created) > sessionTTL {
		delete(a.sessions, token)
		return false
	}
	s.lastSeen = time.Now()
	return true
}

func (a *AuthStore) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// ── Middleware ────────────────────────────────────────────────────────────────

func (a *AuthStore) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !a.ValidSession(cookie.Value) {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}
