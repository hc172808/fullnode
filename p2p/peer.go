package p2p

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type MsgType string

const (
	MsgHandshake     MsgType = "handshake"
	MsgGetStatus     MsgType = "getStatus"
	MsgStatus        MsgType = "status"
	MsgGetBlocks     MsgType = "getBlocks"
	MsgBlocks        MsgType = "blocks"
	MsgNewBlock      MsgType = "newBlock"
	MsgNewTx         MsgType = "newTx"
	MsgPing          MsgType = "ping"
	MsgPong          MsgType = "pong"
	MsgAuthChallenge MsgType = "authChallenge" // server → client: {nonce}
	MsgAuthResponse  MsgType = "authResponse"  // client → server: {nodeId, signature}
	MsgAuthOk        MsgType = "authOk"        // server → client: {}
	MsgAuthDenied    MsgType = "authDenied"    // server → client: {reason}
)

type Message struct {
	Type    MsgType         `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PeerInfo is exchanged during the initial handshake to share chain state.
type PeerInfo struct {
	ID       string `json:"id"`
	ChainID  int64  `json:"chainId"`
	Height   uint64 `json:"height"`
	NodeMode string `json:"nodeMode"`
	Version  string `json:"version"`
	NodeID   string `json:"nodeId,omitempty"` // ed25519 public key (hex)
}

// AuthChallengePayload is sent server→client to start the auth handshake.
type AuthChallengePayload struct {
	Nonce string `json:"nonce"` // hex-encoded 32-byte random nonce
}

// AuthResponsePayload is sent client→server as proof of identity.
type AuthResponsePayload struct {
	NodeID    string `json:"nodeId"`    // hex ed25519 public key
	Signature string `json:"signature"` // hex ed25519 sig of nonce bytes
}

// AuthDeniedPayload explains why a connection was rejected.
type AuthDeniedPayload struct {
	Reason string `json:"reason"`
}

// PeerStatus is the externally visible state of a connected peer.
type PeerStatus struct {
	Addr       string `json:"addr"`
	NodeID     string `json:"nodeId"`
	Height     uint64 `json:"height"`
	NodeMode   string `json:"nodeMode"`
	Version    string `json:"version"`
	Authorized bool   `json:"authorized"` // always true once past auth; false until verified
}

// Peer represents a single TCP connection to a remote node.
type Peer struct {
	mu     sync.Mutex
	conn   net.Conn
	info   *PeerInfo
	sendCh chan Message
	quit   chan struct{}
	onMsg  func(*Peer, Message)
	// auth state (set once, then read-only)
	challenge  string // hex nonce we sent to this peer
	peerNodeID string // verified Node ID of the remote peer
	authorized bool   // true after challenge-response is verified (or auth is off)
	closeOnce  sync.Once
	onClose    func(*Peer)
}

func NewPeer(conn net.Conn, onMsg func(*Peer, Message)) *Peer {
	return &Peer{
		conn:   conn,
		sendCh: make(chan Message, 64),
		quit:   make(chan struct{}),
		onMsg:  onMsg,
	}
}

func (p *Peer) Start() {
	go p.readLoop()
	go p.writeLoop()
	go p.pingLoop()
}

func (p *Peer) Send(msg Message) {
	select {
	case p.sendCh <- msg:
	default:
		log.Warn().Str("peer", p.RemoteAddr()).Msg("send channel full, dropping message")
	}
}

func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		close(p.quit)
		p.conn.Close()
		if p.onClose != nil {
			p.onClose(p)
		}
	})
}

func (p *Peer) RemoteAddr() string {
	return p.conn.RemoteAddr().String()
}

func (p *Peer) readLoop() {
	dec := json.NewDecoder(p.conn)
	for {
		var msg Message
		if err := dec.Decode(&msg); err != nil {
			select {
			case <-p.quit:
			default:
				log.Debug().Err(err).Str("peer", p.RemoteAddr()).Msg("peer read error")
				p.Close()
			}
			return
		}
		if p.onMsg != nil {
			p.onMsg(p, msg)
		}
	}
}

func (p *Peer) writeLoop() {
	enc := json.NewEncoder(p.conn)
	for {
		select {
		case <-p.quit:
			return
		case msg := <-p.sendCh:
			p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := enc.Encode(msg); err != nil {
				log.Debug().Err(err).Str("peer", p.RemoteAddr()).Msg("peer write error")
				p.Close()
				return
			}
		}
	}
}

func (p *Peer) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-ticker.C:
			p.Send(Message{Type: MsgPing})
		}
	}
}

// GetBlocksPayload is the wire payload for a MsgGetBlocks request.
type GetBlocksPayload struct {
	From  uint64 `json:"from"`
	Count int    `json:"count"`
}

// BlockFetcher is called by the server when a peer requests a block range.
type BlockFetcher func(from uint64, count int) json.RawMessage

// ── Server ────────────────────────────────────────────────────────────────────

type Server struct {
	mu            sync.RWMutex
	peers         map[string]*Peer
	port          int
	chainID       int64
	nodeMode      string
	advertiseHost string
	height        func() uint64
	onMsg         func(*Peer, Message)
	blockProv     BlockFetcher
	quit          chan struct{}

	// Auth — set via SetAuth before Start().
	nodeKey      *NodeKey            // our own identity (nil = no auth)
	peerAuth     bool                // if true, require challenge-response
	allowedNodes map[string]struct{} // whitelist of permitted node IDs
}

func NewServer(port int, chainID int64, height func() uint64) *Server {
	return &Server{
		peers:        make(map[string]*Peer),
		port:         port,
		chainID:      chainID,
		nodeMode:     "full",
		height:       height,
		quit:         make(chan struct{}),
		allowedNodes: make(map[string]struct{}),
	}
}

// SetNodeMode records the local node mode for peer handshakes.
// Call this before Start so connected peers see the configured role.
func (s *Server) SetNodeMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == "" {
		mode = "full"
	}
	s.nodeMode = mode
}

// SetAdvertiseHost configures the public host included in the node's enode.
// The listener still binds on all interfaces; this value is only for peers
// and wallet/operator diagnostics.
func (s *Server) SetAdvertiseHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advertiseHost = strings.TrimSpace(host)
}

// SetAuth configures peer authorization. Call before Start().
//   - nk:           this node's keypair (must be non-nil when auth is enabled)
//   - requireAuth:  if true, all inbound peers must prove their identity
//   - allowedIDs:   whitelist of permitted node IDs; empty = allow all authenticated nodes
func (s *Server) SetAuth(nk *NodeKey, requireAuth bool, allowedIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeKey = nk
	s.peerAuth = requireAuth
	s.allowedNodes = make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		if id = strings.TrimSpace(id); id != "" {
			s.allowedNodes[id] = struct{}{}
		}
	}
}

// NodeID returns this node's P2P identity string, or "" if no key is loaded.
func (s *Server) NodeID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodeKey == nil {
		return ""
	}
	return s.nodeKey.ID()
}

func (s *Server) OnMessage(fn func(*Peer, Message)) {
	s.onMsg = fn
}

// SetBlockProvider registers a callback used to serve MsgGetBlocks requests.
func (s *Server) SetBlockProvider(fn BlockFetcher) {
	s.mu.Lock()
	s.blockProv = fn
	s.mu.Unlock()
}

// RequestBlocks broadcasts a MsgGetBlocks request to all connected peers.
func (s *Server) RequestBlocks(from uint64, count int) {
	payload, _ := json.Marshal(GetBlocksPayload{From: from, Count: count})
	s.Broadcast(Message{Type: MsgGetBlocks, Payload: payload})
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("p2p listen: %w", err)
	}
	s.mu.RLock()
	authMode := s.peerAuth
	s.mu.RUnlock()
	log.Info().Int("port", s.port).Bool("peerAuth", authMode).Msg("P2P server listening")
	go s.acceptLoop(ln)
	return nil
}

// randomNonce generates a 32-byte random nonce and returns it hex-encoded.
func randomNonce() string {
	b := make([]byte, 32)
	rand.Read(b) //nolint:errcheck — crypto/rand never fails on supported platforms
	return hex.EncodeToString(b)
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return
			default:
				log.Error().Err(err).Msg("accept error")
				continue
			}
		}
		go s.onNewConn(conn, false)
	}
}

// onNewConn handles both inbound (outbound=false) and outbound (outbound=true) connections.
func (s *Server) onNewConn(conn net.Conn, outbound bool) {
	peer := NewPeer(conn, s.handleMessage)
	addr := conn.RemoteAddr().String()

	s.mu.RLock()
	nk := s.nodeKey
	requireAuth := s.peerAuth
	nodeMode := s.nodeMode
	s.mu.RUnlock()

	// Register peer immediately so we can receive their handshake.
	s.mu.Lock()
	s.peers[addr] = peer
	peer.onClose = func(closed *Peer) {
		s.mu.Lock()
		if current, ok := s.peers[addr]; ok && current == closed {
			delete(s.peers, addr)
		}
		s.mu.Unlock()
	}
	s.mu.Unlock()

	peer.Start()

	// Build our handshake payload.
	myNodeID := ""
	if nk != nil {
		myNodeID = nk.ID()
	}
	hs, _ := json.Marshal(PeerInfo{
		ChainID:  s.chainID,
		Height:   s.height(),
		NodeMode: nodeMode,
		Version:  "1.0.0",
		NodeID:   myNodeID,
	})
	peer.Send(Message{Type: MsgHandshake, Payload: hs})

	// If auth is enabled and this is an inbound connection, send a challenge.
	if requireAuth && !outbound {
		nonce := randomNonce()
		peer.mu.Lock()
		peer.challenge = nonce
		peer.mu.Unlock()
		challenge, _ := json.Marshal(AuthChallengePayload{Nonce: nonce})
		peer.Send(Message{Type: MsgAuthChallenge, Payload: challenge})
		log.Debug().Str("peer", addr).Str("nonce", nonce[:8]+"…").Msg("sent auth challenge")
	} else {
		// No auth required — peer is immediately considered authorized.
		peer.mu.Lock()
		peer.authorized = true
		peer.mu.Unlock()
	}

	if outbound {
		log.Info().Str("addr", addr).Bool("authRequired", requireAuth).Msg("connected to bootstrap peer")
	} else {
		log.Info().Str("peer", addr).Bool("authRequired", requireAuth).Msg("new peer connected")
	}
}

func (s *Server) handleMessage(peer *Peer, msg Message) {
	switch msg.Type {
	case MsgPing:
		peer.Send(Message{Type: MsgPong})

	case MsgPong:
		// keep-alive reply — no action needed

	case MsgHandshake:
		var info PeerInfo
		if err := json.Unmarshal(msg.Payload, &info); err != nil {
			log.Warn().Err(err).Str("peer", peer.RemoteAddr()).Msg("invalid peer handshake")
			peer.Close()
			return
		}
		if info.ChainID != s.chainID {
			log.Warn().
				Str("peer", peer.RemoteAddr()).
				Int64("localChainId", s.chainID).
				Int64("remoteChainId", info.ChainID).
				Msg("peer rejected — chain ID mismatch")
			peer.Close()
			return
		}
		peer.mu.Lock()
		peer.info = &info
		// If the remote included its node ID and we aren't in auth mode,
		// record it for display purposes.
		if info.NodeID != "" && peer.peerNodeID == "" {
			peer.peerNodeID = info.NodeID
		}
		peer.mu.Unlock()
		log.Info().Str("peer", peer.RemoteAddr()).Str("nodeId", info.NodeID).Int64("chainId", info.ChainID).Msg("peer handshake accepted")

	case MsgAuthChallenge:
		// We received a challenge from the remote node — sign it and respond.
		s.mu.RLock()
		nk := s.nodeKey
		s.mu.RUnlock()
		if nk == nil {
			log.Warn().Str("peer", peer.RemoteAddr()).Msg("received auth challenge but we have no node key — cannot respond")
			return
		}
		var cp AuthChallengePayload
		if err := json.Unmarshal(msg.Payload, &cp); err != nil || cp.Nonce == "" {
			return
		}
		nonceBytes, err := hex.DecodeString(cp.Nonce)
		if err != nil {
			return
		}
		sig := nk.Sign(nonceBytes)
		resp, _ := json.Marshal(AuthResponsePayload{NodeID: nk.ID(), Signature: sig})
		peer.Send(Message{Type: MsgAuthResponse, Payload: resp})
		log.Debug().Str("peer", peer.RemoteAddr()).Msg("sent auth response")

	case MsgAuthResponse:
		// Verify the remote's challenge-response and decide whether to accept.
		peer.mu.Lock()
		challenge := peer.challenge
		peer.mu.Unlock()

		if challenge == "" {
			// We never sent a challenge — ignore (might be an outbound peer echoing)
			return
		}

		var resp AuthResponsePayload
		if err := json.Unmarshal(msg.Payload, &resp); err != nil || resp.NodeID == "" {
			s.denyPeer(peer, "malformed auth response")
			return
		}

		nonceBytes, err := hex.DecodeString(challenge)
		if err != nil {
			s.denyPeer(peer, "internal nonce error")
			return
		}

		// Verify the signature.
		if !VerifyNodeSig(resp.NodeID, nonceBytes, resp.Signature) {
			log.Warn().Str("peer", peer.RemoteAddr()).Str("nodeId", resp.NodeID[:min8(resp.NodeID)]+"…").
				Msg("peer auth failed: invalid signature")
			s.denyPeer(peer, "invalid signature")
			return
		}

		// Check whitelist (empty whitelist = allow any valid identity).
		s.mu.RLock()
		allowed := s.allowedNodes
		s.mu.RUnlock()
		if len(allowed) > 0 {
			if _, ok := allowed[resp.NodeID]; !ok {
				log.Warn().Str("peer", peer.RemoteAddr()).Str("nodeId", resp.NodeID[:min8(resp.NodeID)]+"…").
					Msg("peer auth failed: node ID not in allowlist")
				s.denyPeer(peer, "node ID not in allowlist")
				return
			}
		}

		// Auth passed.
		peer.mu.Lock()
		peer.peerNodeID = resp.NodeID
		peer.authorized = true
		peer.mu.Unlock()
		peer.Send(Message{Type: MsgAuthOk})
		log.Info().Str("peer", peer.RemoteAddr()).Str("nodeId", resp.NodeID[:min8(resp.NodeID)]+"…").
			Msg("peer authorized ✓")

	case MsgAuthOk:
		// Server accepted our auth response — mark ourselves as authorized.
		peer.mu.Lock()
		peer.authorized = true
		peer.mu.Unlock()
		log.Info().Str("peer", peer.RemoteAddr()).Msg("auth accepted by remote node ✓")

	case MsgAuthDenied:
		var dp AuthDeniedPayload
		_ = json.Unmarshal(msg.Payload, &dp)
		log.Warn().Str("peer", peer.RemoteAddr()).Str("reason", dp.Reason).
			Msg("connection denied by remote node — disconnecting")
		// Remove from peer map before closing.
		s.mu.Lock()
		delete(s.peers, peer.RemoteAddr())
		s.mu.Unlock()
		peer.Close()

	case MsgGetBlocks:
		// Only serve blocks to authorized peers.
		peer.mu.Lock()
		auth := peer.authorized
		peer.mu.Unlock()
		if !auth {
			return
		}
		s.mu.RLock()
		prov := s.blockProv
		s.mu.RUnlock()
		if prov != nil {
			var req GetBlocksPayload
			if err := json.Unmarshal(msg.Payload, &req); err == nil && req.Count > 0 {
				if req.Count > 200 {
					req.Count = 200
				}
				if payload := prov(req.From, req.Count); len(payload) > 0 {
					peer.Send(Message{Type: MsgBlocks, Payload: payload})
				}
			}
		}

	default:
		// Forward to user-registered handler only for authorized peers.
		peer.mu.Lock()
		auth := peer.authorized
		peer.mu.Unlock()
		if auth && s.onMsg != nil {
			s.onMsg(peer, msg)
		}
	}
}

// denyPeer sends a rejection message and immediately drops the connection.
func (s *Server) denyPeer(peer *Peer, reason string) {
	payload, _ := json.Marshal(AuthDeniedPayload{Reason: reason})
	peer.Send(Message{Type: MsgAuthDenied, Payload: payload})
	// Small delay so the message is flushed before close.
	time.AfterFunc(200*time.Millisecond, func() {
		s.mu.Lock()
		delete(s.peers, peer.RemoteAddr())
		s.mu.Unlock()
		peer.Close()
	})
}

func (s *Server) Broadcast(msg Message) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.peers {
		p.mu.Lock()
		auth := p.authorized
		p.mu.Unlock()
		if auth {
			p.Send(msg)
		}
	}
}

func (s *Server) PeerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, p := range s.peers {
		p.mu.Lock()
		authorized := p.authorized
		p.mu.Unlock()
		if authorized {
			count++
		}
	}
	return count
}

// P2PPort returns the TCP port on which this node listens for peers.
func (s *Server) P2PPort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// Enode returns a stable Ethereum-style endpoint for this node. An empty
// result means the operator has not configured a public advertised host.
func (s *Server) Enode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodeKey == nil || s.advertiseHost == "" {
		return ""
	}
	return fmt.Sprintf("enode://%s@%s", s.nodeKey.ID(), net.JoinHostPort(s.advertiseHost, fmt.Sprintf("%d", s.port)))
}

// Peers returns the current status of all connected peers.
func (s *Server) Peers() []PeerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PeerStatus, 0, len(s.peers))
	for addr, p := range s.peers {
		p.mu.Lock()
		if !p.authorized {
			p.mu.Unlock()
			continue
		}
		ps := PeerStatus{
			Addr:       addr,
			NodeID:     p.peerNodeID,
			Authorized: p.authorized,
		}
		if p.info != nil {
			ps.Height = p.info.Height
			ps.NodeMode = p.info.NodeMode
			ps.Version = p.info.Version
		}
		p.mu.Unlock()
		out = append(out, ps)
	}
	return out
}

// NormalizeAddr strips the optional tcp:// scheme from a peer address.
func NormalizeAddr(addr string) string {
	addr = strings.TrimPrefix(addr, "tcp://")
	addr = strings.TrimPrefix(addr, "TCP://")
	return strings.TrimSpace(addr)
}

// ConnectTo dials a bootstrap peer. addr must be in host:port form (no scheme).
func (s *Server) ConnectTo(addr string) error {
	addr = NormalizeAddr(addr)
	if addr == "" {
		return fmt.Errorf("empty peer address")
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	go s.onNewConn(conn, true)
	return nil
}

// MaxPeerHeight returns the highest block height reported by any connected peer.
func (s *Server) MaxPeerHeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var max uint64
	for _, p := range s.peers {
		p.mu.Lock()
		if p.info != nil && p.info.Height > max {
			max = p.info.Height
		}
		p.mu.Unlock()
	}
	return max
}

// min8 returns min(8, len(s)) for safe log truncation.
func min8(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
}
