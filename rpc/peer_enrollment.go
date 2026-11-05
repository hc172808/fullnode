package rpc

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gydschain/fullnode/config"
	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
	"github.com/rs/zerolog/log"
)

const (
	peerRegistrationTable       = "peer_registrations"
	maxPeerEnrollmentRecords    = 512
	maxPeerEnrollmentChallenges = 2048
	peerEnrollmentChallengeTTL  = 2 * time.Minute
)

type peerEnrollmentClaims struct {
	NodeID      string `json:"nodeId"`
	Network     string `json:"network"`
	ChainID     int64  `json:"chainId"`
	GenesisHash string `json:"genesisHash"`
	NodeMode    string `json:"nodeMode"`
	P2PPort     int    `json:"p2pPort"`
}

type peerEnrollmentChallenge struct {
	Claims    peerEnrollmentClaims
	RemoteIP  string
	Message   string
	ExpiresAt time.Time
}

type peerEnrollmentSubmission struct {
	peerEnrollmentClaims
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type peerEnrollmentRecord struct {
	NodeID      string `json:"nodeId"`
	Network     string `json:"network"`
	ChainID     int64  `json:"chainId"`
	GenesisHash string `json:"genesisHash"`
	NodeMode    string `json:"nodeMode"`
	Address     string `json:"address"`
	Status      string `json:"status"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
	ApprovedAt  string `json:"approvedAt,omitempty"`
}

type peerRegistrationSigner interface {
	SignNodeMessage([]byte) (nodeID, signature string, ok bool)
}

type runtimePeerAuthorizer interface {
	SetAllowedNodes([]string)
}

type peerNodeDisconnector interface {
	DisconnectNode(nodeID string) bool
}

type peerEnrollmentHTTPError struct {
	Status int
	Body   string
}

func (e *peerEnrollmentHTTPError) Error() string {
	return fmt.Sprintf("peer enrollment endpoint returned HTTP %d: %s", e.Status, e.Body)
}

// LoadApprovedPeerIDs reads the enrollment approval registry before P2P starts.
// The admin database is closed before NewServer opens its long-lived handle.
func LoadApprovedPeerIDs(dataDir string) ([]string, error) {
	approved, _, err := LoadPeerAuthorizationIDs(dataDir)
	return approved, err
}

// LoadPeerAuthorizationIDs returns approved and revoked enrolled identities.
// Revocations override the legacy environment allowlist on startup.
func LoadPeerAuthorizationIDs(dataDir string) (approved, revoked []string, err error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, nil, nil
	}
	db, err := NewAdminDB(dataDir)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	records, err := peerEnrollmentRecordsFromDB(db)
	if err != nil {
		return nil, nil, err
	}
	for _, record := range records {
		switch record.Status {
		case "approved":
			approved = append(approved, record.NodeID)
		case "revoked":
			revoked = append(revoked, record.NodeID)
		}
	}
	sort.Strings(approved)
	sort.Strings(revoked)
	return approved, revoked, nil
}

func approvedPeerIDsFromDB(db *AdminDB) ([]string, error) {
	records, err := peerEnrollmentRecordsFromDB(db)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		if record.Status == "approved" {
			ids = append(ids, record.NodeID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func peerEnrollmentRecordsFromDB(db *AdminDB) ([]peerEnrollmentRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("peer registry is unavailable")
	}
	entries, err := db.Records(peerRegistrationTable)
	if err != nil {
		return nil, err
	}
	records := make([]peerEnrollmentRecord, 0, len(entries))
	for _, entry := range entries {
		var record peerEnrollmentRecord
		if err := json.Unmarshal(entry.Value, &record); err != nil {
			return nil, fmt.Errorf("decode peer enrollment %q: %w", entry.Key, err)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].LastSeen > records[j].LastSeen
	})
	return records, nil
}

func (s *Server) savePeerEnrollment(record peerEnrollmentRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.adminDB.PutRecord(peerRegistrationTable, record.NodeID, raw)
	return err
}

func (s *Server) findPeerEnrollment(nodeID string) (peerEnrollmentRecord, bool, error) {
	records, err := peerEnrollmentRecordsFromDB(s.adminDB)
	if err != nil {
		return peerEnrollmentRecord{}, false, err
	}
	for _, record := range records {
		if record.NodeID == nodeID {
			return record, true, nil
		}
	}
	return peerEnrollmentRecord{}, false, nil
}

func normalizeEnrollmentClaims(claims peerEnrollmentClaims) (peerEnrollmentClaims, error) {
	claims.NodeID = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(claims.NodeID)), "0x")
	if len(claims.NodeID) != 64 {
		return claims, fmt.Errorf("node ID must be a 32-byte Ed25519 public key")
	}
	if _, err := hex.DecodeString(claims.NodeID); err != nil {
		return claims, fmt.Errorf("node ID is not valid hexadecimal")
	}
	claims.Network = strings.ToLower(strings.TrimSpace(claims.Network))
	claims.GenesisHash = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(claims.GenesisHash)), "0x")
	claims.NodeMode = strings.ToLower(strings.TrimSpace(claims.NodeMode))
	mode, err := config.NormalizeNodeMode(claims.NodeMode)
	if err != nil || !config.PeerAuthRequired(mode) {
		return claims, fmt.Errorf("only P2P-enabled node roles can enroll")
	}
	claims.NodeMode = mode
	if claims.ChainID <= 0 {
		return claims, fmt.Errorf("chain ID must be positive")
	}
	if claims.GenesisHash == "" {
		return claims, fmt.Errorf("genesis hash is required")
	}
	if claims.P2PPort < 1 || claims.P2PPort > 65535 {
		return claims, fmt.Errorf("P2P port must be between 1 and 65535")
	}
	return claims, nil
}

func enrollmentMessage(claims peerEnrollmentClaims, nonce string) string {
	return fmt.Sprintf(
		"GYDS peer enrollment v1\nnode_id=%s\nnetwork=%s\nchain_id=%d\ngenesis_hash=%s\nnode_mode=%s\np2p_port=%d\nnonce=%s\n",
		claims.NodeID, claims.Network, claims.ChainID, claims.GenesisHash,
		claims.NodeMode, claims.P2PPort, nonce,
	)
}

func (s *Server) validateEnrollmentNetwork(claims peerEnrollmentClaims) error {
	if s.chain == nil || s.chain.GenesisConfig() == nil {
		return fmt.Errorf("node chain identity is unavailable")
	}
	genesis := s.chain.GenesisConfig()
	expectedNetwork := strings.ToLower(strings.TrimSpace(config.FromEnv().Network))
	expectedHash := strings.ToLower(strings.TrimPrefix(core.GenesisBlock(genesis).Hash, "0x"))
	if claims.Network != expectedNetwork ||
		claims.ChainID != genesis.ChainID ||
		claims.GenesisHash != expectedHash {
		return fmt.Errorf("network, chain ID, or genesis hash does not match this node")
	}
	return nil
}

func decodeEnrollmentJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("request must contain one JSON object")
		}
		return err
	}
	return nil
}

func enrollmentRemoteIP(r *http.Request) (net.IP, error) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("could not determine peer address")
	}
	// Trust forwarded IP headers only from a same-host reverse proxy. Registration
	// still requires the resulting address to be private/VPN, never public.
	if ip.IsLoopback() {
		for _, header := range []string{"X-Real-IP", "X-Forwarded-For"} {
			value := strings.TrimSpace(strings.SplitN(r.Header.Get(header), ",", 2)[0])
			if forwarded := net.ParseIP(strings.Trim(value, "[]")); forwarded != nil {
				ip = forwarded
				break
			}
		}
	}
	if !privateOrVPNIP(ip) {
		return nil, fmt.Errorf("peer enrollment is accepted only from a private or VPN address")
	}
	return ip, nil
}

func privateOrVPNIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ip != nil
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(ip)
}

func (s *Server) handlePeerEnrollmentChallenge(w http.ResponseWriter, r *http.Request) {
	remoteIP, err := enrollmentRemoteIP(r)
	if err != nil {
		jsonErr(w, http.StatusForbidden, err.Error())
		return
	}
	var claims peerEnrollmentClaims
	if err := decodeEnrollmentJSON(w, r, &claims); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid enrollment request")
		return
	}
	claims, err = normalizeEnrollmentClaims(claims)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateEnrollmentNetwork(claims); err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not create enrollment challenge")
		return
	}
	nonce := hex.EncodeToString(nonceBytes)
	message := enrollmentMessage(claims, nonce)
	now := time.Now()

	s.peerChallengeMu.Lock()
	if s.peerChallenges == nil {
		s.peerChallenges = make(map[string]peerEnrollmentChallenge)
	}
	for key, challenge := range s.peerChallenges {
		if now.After(challenge.ExpiresAt) {
			delete(s.peerChallenges, key)
		}
	}
	if len(s.peerChallenges) >= maxPeerEnrollmentChallenges {
		s.peerChallengeMu.Unlock()
		jsonErr(w, http.StatusTooManyRequests, "too many enrollment challenges; retry shortly")
		return
	}
	s.peerChallenges[nonce] = peerEnrollmentChallenge{
		Claims: claims, RemoteIP: remoteIP.String(), Message: message,
		ExpiresAt: now.Add(peerEnrollmentChallengeTTL),
	}
	s.peerChallengeMu.Unlock()

	jsonOK(w, map[string]interface{}{
		"nonce": nonce, "message": message,
		"expiresAt": now.Add(peerEnrollmentChallengeTTL).UTC().Format(time.RFC3339),
	})
}

func (s *Server) handlePeerEnrollmentSubmit(w http.ResponseWriter, r *http.Request) {
	remoteIP, err := enrollmentRemoteIP(r)
	if err != nil {
		jsonErr(w, http.StatusForbidden, err.Error())
		return
	}
	var submission peerEnrollmentSubmission
	if err := decodeEnrollmentJSON(w, r, &submission); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid enrollment submission")
		return
	}
	claims, err := normalizeEnrollmentClaims(submission.peerEnrollmentClaims)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateEnrollmentNetwork(claims); err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}

	s.peerChallengeMu.Lock()
	challenge, exists := s.peerChallenges[submission.Nonce]
	if exists {
		delete(s.peerChallenges, submission.Nonce)
	}
	s.peerChallengeMu.Unlock()
	if !exists || time.Now().After(challenge.ExpiresAt) ||
		challenge.RemoteIP != remoteIP.String() ||
		challenge.Message != enrollmentMessage(claims, submission.Nonce) {
		jsonErr(w, http.StatusUnauthorized, "enrollment challenge is invalid or expired")
		return
	}
	if !p2p.VerifyNodeSig(claims.NodeID, []byte(challenge.Message), submission.Signature) {
		jsonErr(w, http.StatusUnauthorized, "node identity signature is invalid")
		return
	}

	address := net.JoinHostPort(remoteIP.String(), strconv.Itoa(claims.P2PPort))
	now := time.Now().UTC().Format(time.RFC3339)
	s.peerEnrollmentMu.Lock()
	defer s.peerEnrollmentMu.Unlock()
	records, err := peerEnrollmentRecordsFromDB(s.adminDB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not load peer registry")
		return
	}
	if len(records) >= maxPeerEnrollmentRecords {
		if _, exists, findErr := s.findPeerEnrollment(claims.NodeID); findErr != nil || !exists {
			jsonErr(w, http.StatusTooManyRequests, "peer registry is full")
			return
		}
	}
	record, found, err := s.findPeerEnrollment(claims.NodeID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not load peer registry")
		return
	}
	status := "pending"
	firstSeen := now
	approvedAt := ""
	removePreviousAddress := false
	if found {
		status = record.Status
		firstSeen = record.FirstSeen
		if status == "approved" && record.Address != address {
			// A changed VPN source address must be reviewed again before it is
			// used as a new bootstrap target.
			status = "pending"
			removePreviousAddress = true
		} else if status == "approved" {
			approvedAt = record.ApprovedAt
		}
	}
	if removePreviousAddress {
		previousPeers := config.LoadBootstrapNodes(s.dataDir)
		if err := config.SaveBootstrapNodes(s.dataDir, removePeerAddress(previousPeers, record.Address)); err != nil {
			jsonErr(w, http.StatusInternalServerError, "could not update the registered peer address")
			return
		}
	}
	record = peerEnrollmentRecord{
		NodeID: claims.NodeID, Network: claims.Network, ChainID: claims.ChainID,
		GenesisHash: claims.GenesisHash, NodeMode: claims.NodeMode, Address: address,
		Status: status, FirstSeen: firstSeen, LastSeen: now, ApprovedAt: approvedAt,
	}
	if err := s.savePeerEnrollment(record); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save peer registration")
		return
	}
	if found && status != "approved" && s.p2p != nil {
		if disconnector, ok := s.p2p.(peerNodeDisconnector); ok {
			disconnector.DisconnectNode(claims.NodeID)
		}
		_ = s.updateRuntimePeerAllowlist()
	}
	jsonOK(w, map[string]interface{}{
		"status":  status,
		"message": "Node identity verified. An administrator must approve this node before it can sync.",
	})
}

func (s *Server) handleAdminPeerRegistrations(w http.ResponseWriter, _ *http.Request) {
	records, err := peerEnrollmentRecordsFromDB(s.adminDB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not load peer registry")
		return
	}
	jsonOK(w, map[string]interface{}{"registrations": records})
}

func (s *Server) handleAdminPeerRegistrationApprove(w http.ResponseWriter, r *http.Request) {
	nodeID, err := normalizeEnrollmentNodeID(mux.Vars(r)["nodeID"])
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.peerEnrollmentMu.Lock()
	defer s.peerEnrollmentMu.Unlock()
	record, found, err := s.findPeerEnrollment(nodeID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not load peer registry")
		return
	}
	if !found {
		jsonErr(w, http.StatusNotFound, "pending peer registration was not found")
		return
	}
	if record.Network != strings.ToLower(strings.TrimSpace(config.FromEnv().Network)) {
		jsonErr(w, http.StatusConflict, "peer registration belongs to a different network")
		return
	}
	if err := s.validateEnrollmentNetwork(peerEnrollmentClaims{
		Network: record.Network, ChainID: record.ChainID, GenesisHash: record.GenesisHash,
	}); err != nil {
		jsonErr(w, http.StatusConflict, "peer registration no longer matches this node's chain identity")
		return
	}
	if !validEnrolledPeerAddress(record.Address) {
		jsonErr(w, http.StatusBadRequest, "registered address is not a private VPN peer endpoint")
		return
	}
	previousPeers := config.LoadBootstrapNodes(s.dataDir)
	nextPeers := appendUniquePeerAddress(previousPeers, record.Address)
	if err := config.SaveBootstrapNodes(s.dataDir, nextPeers); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save approved peer address")
		return
	}
	if record.Status != "approved" {
		record.ApprovedAt = time.Now().UTC().Format(time.RFC3339)
	}
	record.Status = "approved"
	if err := s.savePeerEnrollment(record); err != nil {
		_ = config.SaveBootstrapNodes(s.dataDir, previousPeers)
		jsonErr(w, http.StatusInternalServerError, "could not save peer approval")
		return
	}
	if err := s.updateRuntimePeerAllowlist(); err != nil {
		jsonErr(w, http.StatusInternalServerError, "approval saved, but runtime authorization could not be updated")
		return
	}
	if s.p2p != nil {
		go func() {
			if err := s.p2p.ConnectTo(record.Address); err != nil {
				log.Warn().Err(err).Str("nodeId", nodeID).Str("address", record.Address).
					Msg("approved peer connection attempt failed")
			}
		}()
	}
	if s.auth != nil {
		s.auth.writeAudit(realIP(r), "PEER_APPROVED:"+nodeID)
	}
	jsonOK(w, map[string]interface{}{
		"status": "approved", "address": record.Address,
		"message": "Node approved. The server saved its VPN address and is attempting a P2P connection.",
	})
}

func (s *Server) handleAdminPeerRegistrationRevoke(w http.ResponseWriter, r *http.Request) {
	nodeID, err := normalizeEnrollmentNodeID(mux.Vars(r)["nodeID"])
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.peerEnrollmentMu.Lock()
	defer s.peerEnrollmentMu.Unlock()
	record, found, err := s.findPeerEnrollment(nodeID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not load peer registry")
		return
	}
	if !found || record.Status != "approved" {
		jsonErr(w, http.StatusNotFound, "approved peer was not found")
		return
	}
	previousPeers := config.LoadBootstrapNodes(s.dataDir)
	nextPeers := removePeerAddress(previousPeers, record.Address)
	if err := config.SaveBootstrapNodes(s.dataDir, nextPeers); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not remove approved peer address")
		return
	}
	record.Status = "revoked"
	record.ApprovedAt = ""
	if err := s.savePeerEnrollment(record); err != nil {
		_ = config.SaveBootstrapNodes(s.dataDir, previousPeers)
		jsonErr(w, http.StatusInternalServerError, "could not revoke peer approval")
		return
	}
	if err := s.updateRuntimePeerAllowlist(); err != nil {
		jsonErr(w, http.StatusInternalServerError, "revocation saved, but runtime authorization could not be updated")
		return
	}
	if disconnector, ok := s.p2p.(peerNodeDisconnector); ok {
		disconnector.DisconnectNode(nodeID)
	}
	if s.auth != nil {
		s.auth.writeAudit(realIP(r), "PEER_REVOKED:"+nodeID)
	}
	jsonOK(w, map[string]string{"status": "revoked", "message": "Node approval revoked and its active P2P session disconnected."})
}

func (s *Server) handleAdminPeerRegistrationsClear(w http.ResponseWriter, r *http.Request) {
	s.peerEnrollmentMu.Lock()
	defer s.peerEnrollmentMu.Unlock()
	records, err := peerEnrollmentRecordsFromDB(s.adminDB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not load peer registry")
		return
	}
	keys := make([]string, 0, len(records))
	removed := make([]peerEnrollmentRecord, 0, len(records))
	addresses := make(map[string]bool)
	for _, record := range records {
		if record.Status != "pending" && record.Status != "approved" {
			continue
		}
		keys = append(keys, record.NodeID)
		removed = append(removed, record)
		addresses[record.Address] = true
	}
	if len(keys) == 0 {
		jsonOK(w, map[string]interface{}{"removed": 0, "message": "There are no pending or approved peer records to clear."})
		return
	}
	previousPeers := config.LoadBootstrapNodes(s.dataDir)
	nextPeers := make([]string, 0, len(previousPeers))
	for _, peer := range previousPeers {
		if !addresses[peer] {
			nextPeers = append(nextPeers, peer)
		}
	}
	if err := config.SaveBootstrapNodes(s.dataDir, nextPeers); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not remove enrolled peer addresses")
		return
	}
	if err := s.adminDB.DeleteRecords(peerRegistrationTable, keys); err != nil {
		_ = config.SaveBootstrapNodes(s.dataDir, previousPeers)
		jsonErr(w, http.StatusInternalServerError, "could not clear peer records")
		return
	}
	if err := s.updateRuntimePeerAllowlist(); err != nil {
		jsonErr(w, http.StatusInternalServerError, "peer records cleared, but runtime authorization could not be updated")
		return
	}
	for _, record := range removed {
		if record.Status == "approved" {
			if disconnector, ok := s.p2p.(peerNodeDisconnector); ok {
				disconnector.DisconnectNode(record.NodeID)
			}
		}
	}
	if s.auth != nil {
		s.auth.writeAudit(realIP(r), "PEER_REGISTRATIONS_CLEARED")
	}
	jsonOK(w, map[string]interface{}{
		"removed": len(keys),
		"message": "Pending and approved enrollment records were cleared. Manually configured peers and blockchain data were left unchanged.",
	})
}

func normalizeEnrollmentNodeID(raw string) (string, error) {
	claims, err := normalizeEnrollmentClaims(peerEnrollmentClaims{
		NodeID: raw, Network: "mainnet", ChainID: 1, GenesisHash: "00",
		NodeMode: "full", P2PPort: 1,
	})
	if err != nil {
		return "", fmt.Errorf("invalid node ID")
	}
	return claims.NodeID, nil
}

func appendUniquePeerAddress(peers []string, address string) []string {
	for _, peer := range peers {
		if strings.EqualFold(strings.TrimSpace(peer), address) {
			return peers
		}
	}
	return append(peers, address)
}

func validEnrolledPeerAddress(address string) bool {
	host, rawPort, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || !privateOrVPNIP(net.ParseIP(host)) {
		return false
	}
	port, err := strconv.Atoi(rawPort)
	return err == nil && port >= 1 && port <= 65535
}

func removePeerAddress(peers []string, address string) []string {
	filtered := make([]string, 0, len(peers))
	for _, peer := range peers {
		if !strings.EqualFold(strings.TrimSpace(peer), address) {
			filtered = append(filtered, peer)
		}
	}
	return filtered
}

func (s *Server) updateRuntimePeerAllowlist() error {
	allowed := append([]string(nil), config.FromEnv().AllowedNodes...)
	records, err := peerEnrollmentRecordsFromDB(s.adminDB)
	if err != nil {
		return err
	}
	approved := make([]string, 0, len(records))
	revoked := make(map[string]bool)
	for _, record := range records {
		switch record.Status {
		case "approved":
			approved = append(approved, record.NodeID)
		case "revoked":
			revoked[record.NodeID] = true
		}
	}
	seen := make(map[string]bool, len(allowed)+len(approved))
	merged := make([]string, 0, len(allowed)+len(approved))
	for _, id := range append(allowed, approved...) {
		id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "0x")
		if id != "" && !revoked[id] && !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	if authorizer, ok := s.p2p.(runtimePeerAuthorizer); ok {
		authorizer.SetAllowedNodes(merged)
	}
	return nil
}

func parsePeerRegistrationBase(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("registration URL is empty")
	}
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("registration URL must be an http(s) origin or base path without credentials, query, or fragment")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	return base, nil
}

func (s *Server) startPeerEnrollmentClient() {
	if s.p2p == nil || s.chain == nil {
		return
	}
	cfg := config.FromEnv()
	if strings.TrimSpace(cfg.PeerRegistrationURL) == "" || !config.PeerAuthRequired(cfg.NodeMode) {
		return
	}
	signer, ok := s.p2p.(peerRegistrationSigner)
	if !ok {
		log.Warn().Msg("peer enrollment URL is configured but P2P signing is unavailable")
		return
	}
	genesis := s.chain.GenesisConfig()
	if genesis == nil {
		return
	}
	nodeID, _, ok := signer.SignNodeMessage([]byte("GYDS peer enrollment identity check"))
	if !ok || nodeID == "" {
		log.Warn().Msg("peer enrollment URL is configured but node identity is unavailable")
		return
	}
	claims := peerEnrollmentClaims{
		NodeID: nodeID, Network: cfg.Network, ChainID: genesis.ChainID,
		GenesisHash: core.GenesisBlock(genesis).Hash, NodeMode: cfg.NodeMode,
		P2PPort: s.p2p.P2PPort(),
	}
	go func() {
		delay := 15 * time.Second
		for {
			err := submitPeerEnrollment(cfg.PeerRegistrationURL, claims, signer)
			if err == nil {
				log.Info().Str("nodeId", nodeID[:16]+"…").Msg("peer enrollment submitted for administrator approval")
				return
			}
			var statusErr *peerEnrollmentHTTPError
			if errors.As(err, &statusErr) && statusErr.Status >= 400 && statusErr.Status < 500 && statusErr.Status != http.StatusTooManyRequests {
				log.Warn().Err(err).Str("nodeId", nodeID[:16]+"…").Msg("peer enrollment was rejected; check the registration URL and network profile")
				return
			}
			log.Warn().Err(err).Str("nodeId", nodeID[:16]+"…").Msg("peer enrollment failed; will retry")
			time.Sleep(delay)
			if delay < 5*time.Minute {
				delay *= 2
			}
		}
	}()
}

func submitPeerEnrollment(rawBase string, claims peerEnrollmentClaims, signer peerRegistrationSigner) error {
	claims, err := normalizeEnrollmentClaims(claims)
	if err != nil {
		return err
	}
	base, err := parsePeerRegistrationBase(rawBase)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	postJSON := func(endpoint string, body interface{}, target interface{}) error {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		u := *base
		u.Path = strings.TrimRight(base.Path, "/") + endpoint
		request, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(raw))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return &peerEnrollmentHTTPError{Status: response.StatusCode, Body: strings.TrimSpace(string(payload))}
		}
		if err := json.Unmarshal(payload, target); err != nil {
			return fmt.Errorf("invalid peer enrollment response: %w", err)
		}
		return nil
	}

	var challenge struct {
		Nonce   string `json:"nonce"`
		Message string `json:"message"`
	}
	if err := postJSON("/api/peer-enrollment/challenge", claims, &challenge); err != nil {
		return err
	}
	nonceBytes, err := hex.DecodeString(challenge.Nonce)
	if err != nil || len(nonceBytes) != 32 ||
		challenge.Message != enrollmentMessage(claims, challenge.Nonce) {
		return fmt.Errorf("registration server returned an invalid challenge")
	}
	nodeID, signature, ok := signer.SignNodeMessage([]byte(challenge.Message))
	if !ok || !strings.EqualFold(nodeID, claims.NodeID) {
		return fmt.Errorf("could not sign enrollment challenge with this node identity")
	}
	submission := peerEnrollmentSubmission{
		peerEnrollmentClaims: claims, Nonce: challenge.Nonce, Signature: signature,
	}
	var result map[string]interface{}
	return postJSON("/api/peer-enrollment", submission, &result)
}
