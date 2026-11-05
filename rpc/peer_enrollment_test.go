package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gydschain/fullnode/config"
	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
)

type enrollmentTestSigner struct {
	key *p2p.NodeKey
}

func (s enrollmentTestSigner) SignNodeMessage(data []byte) (string, string, bool) {
	if s.key == nil {
		return "", "", false
	}
	return s.key.ID(), s.key.Sign(data), true
}

type enrollmentTestP2P struct {
	mu           sync.Mutex
	key          *p2p.NodeKey
	allowed      []string
	connected    []string
	disconnected []string
}

func (p *enrollmentTestP2P) ConnectTo(address string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = append(p.connected, address)
	return nil
}
func (p *enrollmentTestP2P) Disconnect(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disconnected = append(p.disconnected, address)
}
func (p *enrollmentTestP2P) PeerCount() int               { return 0 }
func (p *enrollmentTestP2P) NodeID() string               { return p.key.ID() }
func (p *enrollmentTestP2P) Enode() string                { return "" }
func (p *enrollmentTestP2P) P2PPort() int                 { return 30303 }
func (p *enrollmentTestP2P) Peers() []p2p.PeerStatus      { return nil }
func (p *enrollmentTestP2P) HandshakePeerCount() int      { return 0 }
func (p *enrollmentTestP2P) MaxPeerHeight() uint64        { return 0 }
func (p *enrollmentTestP2P) SetAllowedNodes(ids []string) { p.allowed = append([]string(nil), ids...) }
func (p *enrollmentTestP2P) DisconnectNode(nodeID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disconnected = append(p.disconnected, nodeID)
	return true
}
func (p *enrollmentTestP2P) SignNodeMessage(data []byte) (string, string, bool) {
	return enrollmentTestSigner{key: p.key}.SignNodeMessage(data)
}

func newPeerEnrollmentTestServer(t *testing.T) (*Server, *enrollmentTestP2P) {
	t.Helper()
	t.Setenv("GYDS_NETWORK", "mainnet")
	t.Setenv("GYDS_NODE_MODE", "full")
	t.Setenv("GYDS_DATA_DIR", "")
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")
	t.Setenv("GYDS_ALLOWED_NODES", "")
	t.Setenv("GYDS_PEER_REGISTRATION_URL", "")

	dataDir := t.TempDir()
	chain := core.NewChain(core.GydsGenesis, dataDir)
	t.Cleanup(func() { chain.Close() })
	db, err := NewAdminDB(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := p2p.LoadOrCreateNodeKey(filepath.Join(dataDir, "hub-key"))
	if err != nil {
		t.Fatal(err)
	}
	p2pServer := &enrollmentTestP2P{key: key}
	s := &Server{
		chain:    chain,
		dataDir:  dataDir,
		auth:     NewAuthStore(dataDir),
		adminDB:  db,
		nodeMode: "full",
		p2p:      p2pServer,
		subs:     make(map[string]*subscriber),
	}
	s.syncGate = true
	s.setupDashboardRoutes()
	return s, p2pServer
}

func requestWithAdminSession(t *testing.T, s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: s.auth.NewSession()})
	response := httptest.NewRecorder()
	s.dashRouter.ServeHTTP(response, req)
	return response
}

func TestPeerSelfEnrollmentRequiresSignatureAndAdminApproval(t *testing.T) {
	s, p2pServer := newPeerEnrollmentTestServer(t)
	registeringKey, err := p2p.LoadOrCreateNodeKey(filepath.Join(t.TempDir(), "joining-node"))
	if err != nil {
		t.Fatal(err)
	}
	claims := peerEnrollmentClaims{
		NodeID: registeringKey.ID(), Network: "mainnet", ChainID: 198282,
		GenesisHash: core.GenesisBlock(core.GydsGenesis).Hash, NodeMode: "full", P2PPort: 30303,
	}

	registrationServer := httptest.NewServer(s.dashRouter)
	defer registrationServer.Close()
	if err := submitPeerEnrollment(registrationServer.URL, claims, enrollmentTestSigner{key: registeringKey}); err != nil {
		t.Fatalf("signed self-registration failed: %v", err)
	}

	record, found, err := s.findPeerEnrollment(registeringKey.ID())
	if err != nil || !found {
		t.Fatalf("registered peer not found: found=%v err=%v", found, err)
	}
	if record.Status != "pending" || record.Address != "127.0.0.1:30303" {
		t.Fatalf("registration = %#v, want pending loopback peer", record)
	}
	if p2pServer.allowed != nil && len(p2pServer.allowed) != 0 {
		t.Fatalf("new registration was approved without admin action: %v", p2pServer.allowed)
	}

	unauthorized := httptest.NewRecorder()
	s.dashRouter.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/admin/peer-registrations", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("peer list without admin session = %d, want 401", unauthorized.Code)
	}

	listed := requestWithAdminSession(t, s, http.MethodGet, "/admin/peer-registrations", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), registeringKey.ID()) {
		t.Fatalf("admin peer list status=%d body=%s", listed.Code, listed.Body.String())
	}

	approve := requestWithAdminSession(t, s, http.MethodPost, "/admin/peer-registrations/"+registeringKey.ID()+"/approve", nil)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approve.Code, approve.Body.String())
	}
	record, found, err = s.findPeerEnrollment(registeringKey.ID())
	if err != nil || !found || record.Status != "approved" {
		t.Fatalf("approved record = %#v, found=%v, err=%v", record, found, err)
	}
	if len(p2pServer.allowed) != 1 || p2pServer.allowed[0] != registeringKey.ID() {
		t.Fatalf("runtime allowlist = %v, want %s", p2pServer.allowed, registeringKey.ID())
	}
	if got := config.LoadBootstrapNodes(s.dataDir); len(got) != 1 || got[0] != record.Address {
		t.Fatalf("saved bootstrap peers = %v, want %s", got, record.Address)
	}

	revoke := requestWithAdminSession(t, s, http.MethodPost, "/admin/peer-registrations/"+registeringKey.ID()+"/revoke", nil)
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revoke.Code, revoke.Body.String())
	}
	record, found, err = s.findPeerEnrollment(registeringKey.ID())
	if err != nil || !found || record.Status != "revoked" {
		t.Fatalf("revoked record = %#v, found=%v, err=%v", record, found, err)
	}
	if len(p2pServer.allowed) != 0 {
		t.Fatalf("runtime allowlist after revoke = %v, want empty", p2pServer.allowed)
	}
}

func TestPeerSelfEnrollmentRejectsPublicSourcesAndBadSignatures(t *testing.T) {
	s, _ := newPeerEnrollmentTestServer(t)
	key, err := p2p.LoadOrCreateNodeKey(filepath.Join(t.TempDir(), "joining-node"))
	if err != nil {
		t.Fatal(err)
	}
	claims := peerEnrollmentClaims{
		NodeID: key.ID(), Network: "mainnet", ChainID: 198282,
		GenesisHash: core.GenesisBlock(core.GydsGenesis).Hash, NodeMode: "full", P2PPort: 30303,
	}

	publicRequest := httptest.NewRequest(http.MethodPost, "/api/peer-enrollment/challenge", strings.NewReader(`{}`))
	publicRequest.RemoteAddr = "203.0.113.7:45000"
	publicResponse := httptest.NewRecorder()
	s.dashRouter.ServeHTTP(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusForbidden {
		t.Fatalf("public-source challenge status=%d, want 403", publicResponse.Code)
	}

	nonce := getEnrollmentChallenge(t, s, claims, "10.8.0.2:45000")
	submission := peerEnrollmentSubmission{
		peerEnrollmentClaims: claims, Nonce: nonce,
		Signature: strings.Repeat("00", 64),
	}
	raw, _ := json.Marshal(submission)
	request := httptest.NewRequest(http.MethodPost, "/api/peer-enrollment", strings.NewReader(string(raw)))
	request.RemoteAddr = "10.8.0.2:45000"
	response := httptest.NewRecorder()
	s.dashRouter.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status=%d, want 401", response.Code)
	}
	if records, err := peerEnrollmentRecordsFromDB(s.adminDB); err != nil || len(records) != 0 {
		t.Fatalf("invalid signature created records: records=%v err=%v", records, err)
	}
}

func TestClearPeerEnrollmentsPreservesManualPeersAndChainData(t *testing.T) {
	s, p2pServer := newPeerEnrollmentTestServer(t)
	manualID := strings.Repeat("a", 64)
	t.Setenv("GYDS_ALLOWED_NODES", manualID+","+strings.Repeat("3", 64))
	now := "2026-11-05T12:00:00Z"
	records := []peerEnrollmentRecord{
		{NodeID: strings.Repeat("1", 64), Network: "mainnet", ChainID: 198282, Address: "10.8.0.2:30303", Status: "pending", FirstSeen: now, LastSeen: now},
		{NodeID: strings.Repeat("2", 64), Network: "mainnet", ChainID: 198282, Address: "10.8.0.3:30303", Status: "approved", FirstSeen: now, LastSeen: now},
		{NodeID: strings.Repeat("3", 64), Network: "mainnet", ChainID: 198282, Address: "10.8.0.4:30303", Status: "revoked", FirstSeen: now, LastSeen: now},
	}
	for _, record := range records {
		if err := s.savePeerEnrollment(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.SaveBootstrapNodes(s.dataDir, []string{
		"10.8.0.2:30303", "10.8.0.3:30303", "10.8.0.4:30303", "manual.example:30303",
	}); err != nil {
		t.Fatal(err)
	}

	response := requestWithAdminSession(t, s, http.MethodDelete, "/admin/peer-registrations", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"removed":2`) {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body.String())
	}
	remaining, err := peerEnrollmentRecordsFromDB(s.adminDB)
	if err != nil || len(remaining) != 1 || remaining[0].Status != "revoked" {
		t.Fatalf("remaining records=%v err=%v, want only revoked history", remaining, err)
	}
	peers := config.LoadBootstrapNodes(s.dataDir)
	if len(peers) != 2 || peers[0] != "10.8.0.4:30303" || peers[1] != "manual.example:30303" {
		t.Fatalf("bootstrap peers after clear=%v", peers)
	}
	if len(p2pServer.allowed) != 1 || p2pServer.allowed[0] != manualID {
		t.Fatalf("runtime allowlist after clear=%v, want manual peer only and revoked identity denied", p2pServer.allowed)
	}
	if s.chain.Height() != 0 {
		t.Fatalf("chain height changed during peer cleanup: %d", s.chain.Height())
	}
}

func TestLoadPeerAuthorizationIDsPersistsApprovedAndRevokedStates(t *testing.T) {
	s, _ := newPeerEnrollmentTestServer(t)
	approvedID := strings.Repeat("a", 64)
	revokedID := strings.Repeat("b", 64)
	for _, record := range []peerEnrollmentRecord{
		{NodeID: approvedID, Network: "mainnet", ChainID: 198282, Status: "approved"},
		{NodeID: revokedID, Network: "mainnet", ChainID: 198282, Status: "revoked"},
	} {
		if err := s.savePeerEnrollment(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.adminDB.Close(); err != nil {
		t.Fatal(err)
	}

	approved, revoked, err := LoadPeerAuthorizationIDs(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(approved) != 1 || approved[0] != approvedID {
		t.Fatalf("persisted approved IDs = %v", approved)
	}
	if len(revoked) != 1 || revoked[0] != revokedID {
		t.Fatalf("persisted revoked IDs = %v", revoked)
	}
}

func getEnrollmentChallenge(t *testing.T, s *Server, claims peerEnrollmentClaims, remoteAddr string) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/peer-enrollment/challenge", strings.NewReader(string(raw)))
	request.RemoteAddr = remoteAddr
	response := httptest.NewRecorder()
	s.dashRouter.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("challenge status=%d body=%s", response.Code, response.Body.String())
	}
	var challenge struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	return challenge.Nonce
}
