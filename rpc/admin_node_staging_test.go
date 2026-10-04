package rpc

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBootstrapPeersNormalizesAndDeduplicates(t *testing.T) {
	got, err := parseBootstrapPeers(" tcp://seed.example.com:30303,\nSEED.example.com:30303, [2001:db8::1]:30303 ")
	if err != nil {
		t.Fatalf("parseBootstrapPeers returned an error: %v", err)
	}
	want := []string{"seed.example.com:30303", "[2001:db8::1]:30303"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBootstrapPeers() = %#v, want %#v", got, want)
	}
	if _, err := parseBootstrapPeers("peer.example.com:not-a-port"); err == nil {
		t.Fatal("parseBootstrapPeers accepted an invalid port")
	}
}

func TestStagedBootstrapChangesCanBeAppliedOrDiscarded(t *testing.T) {
	s := &Server{}
	if err := s.stageBootstrapPeer("mainnet", "tcp://new.example.com:30303"); err != nil {
		t.Fatalf("stageBootstrapPeer returned an error: %v", err)
	}
	if err := s.stageBootstrapRemoval("mainnet", "old.example.com:30303"); err != nil {
		t.Fatalf("stageBootstrapRemoval returned an error: %v", err)
	}

	changes := s.pendingBootstrapChanges("mainnet")
	got := applyStagedBootstrapChanges(
		[]string{"old.example.com:30303", "keep.example.com:30303"},
		changes,
	)
	want := []string{"keep.example.com:30303", "new.example.com:30303"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("applied peers = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(changes.Added, []string{"new.example.com:30303"}) ||
		!reflect.DeepEqual(changes.Removed, []string{"old.example.com:30303"}) {
		t.Fatalf("staging unexpectedly changed: %#v", changes)
	}

	s.clearPendingBootstrapChanges("mainnet")
	if got := s.pendingBootstrapChanges("mainnet"); len(got.Added)+len(got.Removed) != 0 {
		t.Fatalf("pending changes were not cleared after apply: %#v", got)
	}
	if got := s.pendingBootstrapChanges("testnet"); len(got.Added)+len(got.Removed) != 0 {
		t.Fatalf("mainnet changes leaked into testnet: %#v", got)
	}
}

func TestImportedPeersRemainPendingUntilApply(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("GYDS_NETWORK", "mainnet")
	t.Setenv("GYDS_DATA_DIR", dataDir)
	t.Setenv("GYDS_ENV_FILE", filepath.Join(dataDir, ".env"))
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")

	s := &Server{}
	request := httptest.NewRequest(
		"POST",
		"/api/nodes/import",
		strings.NewReader(`{"version":"1.1","nodes":[{"name":"Seed","address":"tcp://seed.example.com:30303"}]}`),
	)
	response := httptest.NewRecorder()
	s.handleNodesImport(response, request)
	if response.Code != 200 {
		t.Fatalf("import status = %d, body = %s", response.Code, response.Body.String())
	}

	var result struct {
		Staged    int `json:"staged"`
		Connected int `json:"connected"`
		Results   []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if result.Staged != 1 || result.Connected != 0 || len(result.Results) != 1 || result.Results[0].Status != "pending-apply" {
		t.Fatalf("unexpected import result: %+v", result)
	}

	configResponse := httptest.NewRecorder()
	s.handleAdminNodeConfig(configResponse, httptest.NewRequest("GET", "/admin/node/config", nil))
	var savedView adminNodeConfig
	if err := json.Unmarshal(configResponse.Body.Bytes(), &savedView); err != nil {
		t.Fatalf("decode staged config: %v", err)
	}
	if !strings.Contains(savedView.BootstrapNodes, "seed.example.com:30303") {
		t.Fatalf("staged peer missing from admin form: %q", savedView.BootstrapNodes)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "admin", "bootstrap-peers.json")); !os.IsNotExist(err) {
		t.Fatalf("import wrote the durable peer file before Apply (stat error: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("import wrote .env before Apply (stat error: %v)", err)
	}
}
