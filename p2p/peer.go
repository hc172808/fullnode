package p2p

import (
	"encoding/json"
	"fmt"
	"net"
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

type Server struct {
	mu        sync.RWMutex
	peers     map[string]*Peer
	port      int
	chainID   int64
	height    func() uint64
	onMsg     func(*Peer, Message)
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
			peer.info = &info
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

func (s *Server) ConnectTo(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	peer := NewPeer(conn, s.handleMessage)
	s.mu.Lock()
	s.peers[addr] = peer
	s.mu.Unlock()
	peer.Start()
	log.Info().Str("addr", addr).Msg("connected to bootstrap peer")
	return nil
}
