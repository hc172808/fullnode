package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
)

type readinessPeer struct {
	peerCount      int
	handshakeCount int
	peerHeight     uint64
}

func (p *readinessPeer) ConnectTo(string) error  { return nil }
func (p *readinessPeer) Disconnect(string)       {}
func (p *readinessPeer) PeerCount() int          { return p.peerCount }
func (p *readinessPeer) NodeID() string          { return "" }
func (p *readinessPeer) Enode() string           { return "" }
func (p *readinessPeer) P2PPort() int            { return 0 }
func (p *readinessPeer) Peers() []p2p.PeerStatus { return nil }
func (p *readinessPeer) MaxPeerHeight() uint64   { return p.peerHeight }
func (p *readinessPeer) HandshakePeerCount() int { return p.handshakeCount }

func TestReadinessBlocksServiceUntilPeerHeightIsReached(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	s.SetNodeMode("full")
	s.RequireSyncForServing(true)
	peer := &readinessPeer{peerCount: 1, handshakeCount: 1, peerHeight: 5}
	s.SetP2P(peer)

	response := httptest.NewRecorder()
	s.rpcRouter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready status = %d, want 503", response.Code)
	}
	var status SyncStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if status.Ready || !status.Syncing || status.TargetBlock != 5 {
		t.Fatalf("sync status = %#v, want syncing toward block 5", status)
	}

	notReady := s.dispatch(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "eth_getBalance",
		Params:  []interface{}{rpcTestRecipient, "latest"},
		ID:      "balance",
	})
	if notReady.Error == nil {
		t.Fatal("state RPC was served before the node was ready")
	}
	errorObject, ok := notReady.Error.(map[string]interface{})
	code, codeOK := errorObject["code"].(int)
	if !ok || !codeOK || code != -32002 {
		t.Fatalf("pre-sync RPC error = %#v, want code -32002", notReady.Error)
	}

	dashboard := httptest.NewRecorder()
	s.handleDashboard(dashboard, httptest.NewRequest(http.MethodGet, "/", nil))
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "Syncing before service") {
		t.Fatalf("pre-sync dashboard did not show sync progress: status=%d", dashboard.Code)
	}
	guard := s.syncReadinessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	blockedAPI := httptest.NewRecorder()
	guard.ServeHTTP(blockedAPI, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if blockedAPI.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-sync state API status = %d, want 503", blockedAPI.Code)
	}
	progressAPI := httptest.NewRecorder()
	guard.ServeHTTP(progressAPI, httptest.NewRequest(http.MethodGet, "/sync-status", nil))
	if progressAPI.Code != http.StatusOK {
		t.Fatalf("pre-sync progress endpoint status = %d, want 200", progressAPI.Code)
	}
	for _, path := range []string{"/admin/login", "/admin/node"} {
		adminResponse := httptest.NewRecorder()
		guard.ServeHTTP(adminResponse, httptest.NewRequest(http.MethodGet, path, nil))
		if adminResponse.Code != http.StatusOK {
			t.Fatalf("pre-sync admin route %s status = %d, want 200", path, adminResponse.Code)
		}
	}

	peer.peerHeight = s.chain.Height()
	ready := httptest.NewRecorder()
	s.rpcRouter.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("/ready status after catch-up = %d, want 200", ready.Code)
	}
}

func TestGenesisNodeServesLocalChainWithoutPeerConnections(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	s.SetNodeMode("genesis")
	s.RequireSyncForServing(true)

	ready := httptest.NewRecorder()
	s.rpcRouter.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("/ready status = %d, want 200 for a genesis node without peers", ready.Code)
	}
	var status SyncStatus
	if err := json.Unmarshal(ready.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if !status.Ready || !status.Synced || status.Source != "local-genesis-chain" {
		t.Fatalf("genesis sync status = %#v, want ready local-chain status", status)
	}

	balance := s.dispatch(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "eth_getBalance",
		Params:  []interface{}{rpcTestRecipient, "latest"},
		ID:      "balance",
	})
	if balance.Error != nil {
		t.Fatalf("genesis state RPC was blocked without peers: %#v", balance.Error)
	}
}
