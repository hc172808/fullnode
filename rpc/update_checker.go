package rpc

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// UpdateChecker polls GitHub releases to detect newer node versions.
// Results are cached and surfaced via GET /api/updates.
type UpdateChecker struct {
	mu           sync.RWMutex
	currentVer   string
	latestVer    string
	releaseURL   string
	releaseNotes string
	updateAvail  bool
	lastChecked  time.Time
	checkError   string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

func NewUpdateChecker(currentVersion string) *UpdateChecker {
	return &UpdateChecker{currentVer: currentVersion}
}

// Start begins background polling. interval is typically 24 hours.
func (u *UpdateChecker) Start(interval time.Duration) {
	// Run immediately then on interval.
	go func() {
		u.check()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			u.check()
		}
	}()
}

// check fetches the latest release from GitHub and updates state.
func (u *UpdateChecker) check() {
	const apiURL = "https://api.github.com/repos/gydschain/fullnode/releases/latest"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		u.mu.Lock()
		u.checkError = "network error: " + err.Error()
		u.lastChecked = time.Now()
		u.mu.Unlock()
		log.Debug().Err(err).Msg("Update check failed (network)")
		return
	}
	defer resp.Body.Close()

	// 404 = repo has no releases yet; treat as up to date.
	if resp.StatusCode == http.StatusNotFound {
		u.mu.Lock()
		u.checkError = ""
		u.lastChecked = time.Now()
		u.updateAvail = false
		u.mu.Unlock()
		return
	}
	if resp.StatusCode != http.StatusOK {
		u.mu.Lock()
		u.checkError = "github API returned " + resp.Status
		u.lastChecked = time.Now()
		u.mu.Unlock()
		return
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		u.mu.Lock()
		u.checkError = "parse error: " + err.Error()
		u.lastChecked = time.Now()
		u.mu.Unlock()
		return
	}

	latest := rel.TagName
	// Strip leading 'v' for comparison.
	cmpLatest := latest
	if len(cmpLatest) > 0 && cmpLatest[0] == 'v' {
		cmpLatest = cmpLatest[1:]
	}
	cmpCurrent := u.currentVer
	if len(cmpCurrent) > 0 && cmpCurrent[0] == 'v' {
		cmpCurrent = cmpCurrent[1:]
	}

	avail := cmpLatest != "" && cmpLatest != cmpCurrent

	u.mu.Lock()
	u.latestVer = latest
	u.releaseURL = rel.HTMLURL
	u.releaseNotes = rel.Body
	u.updateAvail = avail
	u.checkError = ""
	u.lastChecked = time.Now()
	u.mu.Unlock()

	if avail {
		log.Info().
			Str("current", u.currentVer).
			Str("latest", latest).
			Str("url", rel.HTMLURL).
			Msg("🔔 Node update available")
	} else {
		log.Debug().Str("version", u.currentVer).Msg("Node is up to date")
	}
}

// Status returns the current update state.
func (u *UpdateChecker) Status() map[string]interface{} {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return map[string]interface{}{
		"currentVersion": u.currentVer,
		"latestVersion":  u.latestVer,
		"updateAvailable": u.updateAvail,
		"releaseUrl":     u.releaseURL,
		"releaseNotes":   u.releaseNotes,
		"lastChecked":    u.lastChecked.UTC().Format(time.RFC3339),
		"error":          u.checkError,
	}
}
