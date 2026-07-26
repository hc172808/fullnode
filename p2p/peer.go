package p2p

import (
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
	MsgHandshake MsgType = "handshake"
	MsgGetStatus MsgType = "getStatus"
	MsgStatus    MsgType = "status"
	MsgGetBlocks MsgType = "getBlocks"
	MsgBlocks    MsgType = "blocks"
	MsgNewBlock  MsgType = "newBlock"
	MsgNewTx     MsgType = "newTx"
	MsgPing      MsgType = "ping"
	MsgPong      MsgType = "pong"
)

type Message struct {
	Type    MsgType         `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type PeerInfo struct {
	ID        string `json:"id"`
	ChainID   int64  `json:"chainId"`
	Height    uint64 `json:"height"`
	NodeMode  string `json:"nodeMode"`
	Version   string `json:"version"`
}

type Peer struct {
	mu      sync.Mutex
	conn    net.Conn
	info    *PeerInfo
	sendCh  chan Message
	quit    chan struct{}
	onMsg   func(*Peer, Message)
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
	close(p.quit)
	p.conn.Close()
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
// It should return the JSON-marshalled []Block slice for blocks [from, from+count).
// Returning nil or an empty payload causes no MsgBlocks reply to be sent.
type BlockFetcher func(from uint64, count int) json.RawMessage

type Server struct {
	mu        sync.RWMutex
	peers     map[string]*Peer
	port      int
	chainID   int64
	height    func() uint64
	onMsg     func(*Peer, Message)
	blockProv BlockFetcher
	quit      chan struct{}
}

func NewServer(port int, chainID int64, height func() uint64) *Server {
	return &Server{
		peers:   make(map[string]*Peer),
		port:    port,
		chainID: chainID,
		height:  height,
		quit:    make(chan struct{}),
	}
}

func (s *Server) OnMessage(fn func(*Peer, Message)) {
	s.onMsg = fn
}

// SetBlockProvider registers a callback used to serve MsgGetBlocks requests from
// remote peers. The callback receives the start block number and desired count and
// must return JSON-marshalled block data (typically a []Block array). If not set,
// incoming MsgGetBlocks requests are silently ignored.
func (s *Server) SetBlockProvider(fn BlockFetcher) {
	s.mu.Lock()
	s.blockProv = fn
	s.mu.Unlock()
}

// RequestBlocks broadcasts a MsgGetBlocks message to all connected peers asking
// for `count` blocks starting at block number `from`.
func (s *Server) RequestBlocks(from uint64, count int) {
	payload, _ := json.Marshal(GetBlocksPayload{From: from, Count: count})
	s.Broadcast(Message{Type: MsgGetBlocks, Payload: payload})
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("p2p listen: %w", err)
	}
	log.Info().Int("port", s.port).Msg("P2P server listening")
	go s.acceptLoop(ln)
	return nil
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
		peer := NewPeer(conn, s.handleMessage)
		s.mu.Lock()
		s.peers[conn.RemoteAddr().String()] = peer
		s.mu.Unlock()
		peer.Start()
		log.Info().Str("peer", conn.RemoteAddr().String()).Msg("new peer connected")
		handshake, _ := json.Marshal(PeerInfo{
			ChainID:  s.chainID,
			Height:   s.height(),
			NodeMode: "lite",
			Version:  "1.0.0",
		})
		peer.Send(Message{Type: MsgHandshake, Payload: handshake})
	}
}

func (s *Server) handleMessage(peer *Peer, msg Message) {
	switch msg.Type {
	case MsgPing:
		peer.Send(Message{Type: MsgPong})
	case MsgPong:
	case MsgHandshake:
		var info PeerInfo
		if err := json.Unmarshal(msg.Payload, &info); err == nil {
			peer.mu.Lock()
			peer.info = &info
			peer.mu.Unlock()
		}
	case MsgGetBlocks:
		// Serve a block range to the requesting peer.
		s.mu.RLock()
		prov := s.blockProv
		s.mu.RUnlock()
		if prov != nil {
			var req GetBlocksPayload
			if err := json.Unmarshal(msg.Payload, &req); err == nil && req.Count > 0 {
				if req.Count > 200 {
					req.Count = 200 // cap to prevent abuse
				}
				if payload := prov(req.From, req.Count); len(payload) > 0 {
					peer.Send(Message{Type: MsgBlocks, Payload: payload})
				}
			}
		}
	default:
		if s.onMsg != nil {
			s.onMsg(peer, msg)
		}
	}
}

func (s *Server) Broadcast(msg Message) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.peers {
		p.Send(msg)
	}
}

func (s *Server) PeerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers)
}

// NormalizeAddr strips the optional tcp:// scheme from a peer address so it
// can be dialled directly by net.Dial. Addresses are assumed to already be
// normalised when they arrive from config.FromEnv (which strips the scheme at
// parse time), but callers that construct addresses ad-hoc can use this helper.
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
	peer := NewPeer(conn, s.handleMessage)
	s.mu.Lock()
	s.peers[addr] = peer
	s.mu.Unlock()
	peer.Start()
	// Send our own handshake so the remote side learns our height and mode.
	handshake, _ := json.Marshal(PeerInfo{
		ChainID:  s.chainID,
		Height:   s.height(),
		NodeMode: "full",
		Version:  "1.0.0",
	})
	peer.Send(Message{Type: MsgHandshake, Payload: handshake})
	log.Info().Str("addr", addr).Msg("connected to bootstrap peer")
	return nil
}

// MaxPeerHeight returns the highest block height reported by any connected peer,
// or 0 if no peers have completed a handshake yet.
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
