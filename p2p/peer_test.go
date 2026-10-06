package p2p

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestEnodeUsesStableNodeIdentityAndPublicAdvertisedHost(t *testing.T) {
	key, err := LoadOrCreateNodeKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(30303, 198282, nil)
	s.SetAuth(key, false, nil)
	s.SetAdvertiseHost("node.example.net")

	want := "enode://" + key.ID() + "@node.example.net:30303"
	if got := s.Enode(); got != want {
		t.Fatalf("Enode() = %q, want %q", got, want)
	}
	if got := s.Enode(); got != want {
		t.Fatalf("Enode() changed between calls: got %q, want %q", got, want)
	}

	for _, host := range []string{"", "localhost", "127.0.0.1", "0.0.0.0", "::", "[::]"} {
		s.SetAdvertiseHost(host)
		if got := s.Enode(); got != "" {
			t.Errorf("Enode() for local/unset host %q = %q, want empty", host, got)
		}
	}
}

func TestHandshakeRejectsChainMismatchAndUnverifiableGenesis(t *testing.T) {
	tests := []struct {
		name string
		info PeerInfo
	}{
		{
			name: "chain mismatch",
			info: PeerInfo{ChainID: 2, GenesisHash: "expected-genesis"},
		},
		{
			name: "genesis mismatch",
			info: PeerInfo{ChainID: 1, GenesisHash: "different-genesis"},
		},
		{
			name: "missing genesis",
			info: PeerInfo{ChainID: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer(30303, 1, nil)
			s.SetGenesisHash("expected-genesis")
			peer, remote := addTestPeer(s, "test-peer")
			defer remote.Close()

			payload, err := json.Marshal(tt.info)
			if err != nil {
				t.Fatal(err)
			}
			s.handleMessage(peer, Message{Type: MsgHandshake, Payload: payload})

			if !peer.isClosed() {
				t.Fatal("incompatible peer connection remained open")
			}
			if got := s.PeerCount(); got != 0 {
				t.Fatalf("PeerCount() = %d after rejection, want 0", got)
			}
		})
	}
}

func TestPeerCloseRemovesOnlyItsMapEntryAndCountsOnlyLiveAuthorizedPeers(t *testing.T) {
	s := NewServer(30303, 1, nil)
	peer, remote := addTestPeer(s, "same-remote-address")
	defer remote.Close()

	if got := s.PeerCount(); got != 0 {
		t.Fatalf("unauthorized PeerCount() = %d, want 0", got)
	}
	if got := len(s.Peers()); got != 0 {
		t.Fatalf("unauthorized Peers() length = %d, want 0", got)
	}

	peer.mu.Lock()
	peer.authorized = true
	peer.mu.Unlock()
	if got := s.PeerCount(); got != 1 {
		t.Fatalf("authorized PeerCount() = %d, want 1", got)
	}

	peer.Close()
	if got := s.PeerCount(); got != 0 {
		t.Fatalf("PeerCount() after close = %d, want 0", got)
	}
	if got := len(s.Peers()); got != 0 {
		t.Fatalf("Peers() after close has %d entries, want 0", got)
	}

	// A stale peer's delayed close must not remove a replacement stored under
	// the same remote address.
	oldPeer, oldRemote := addTestPeer(s, "reused-address")
	newPeer, newRemote := addTestPeer(s, "reused-address")
	defer oldRemote.Close()
	defer newRemote.Close()
	oldPeer.Close()
	s.mu.RLock()
	current := s.peers["reused-address"]
	s.mu.RUnlock()
	if current != newPeer {
		t.Fatal("closing stale peer removed its replacement from the peer map")
	}
	newPeer.Close()
}

func addTestPeer(s *Server, addr string) (*Peer, net.Conn) {
	local, remote := net.Pipe()
	peer := NewPeer(local, s.handleMessage)
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
	return peer, remote
}

func servePeerTestServer(t *testing.T, s *Server, ln net.Listener) string {
	t.Helper()
	s.mu.Lock()
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.mu.Unlock()
	go s.acceptLoop(ln)
	t.Cleanup(func() {
		close(s.quit)
		_ = ln.Close()
		s.mu.RLock()
		peers := make([]*Peer, 0, len(s.peers))
		for _, peer := range s.peers {
			peers = append(peers, peer)
		}
		s.mu.RUnlock()
		for _, peer := range peers {
			peer.Close()
		}
	})
	return ln.Addr().String()
}

func listenPeerTestServer(t *testing.T, s *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return servePeerTestServer(t, s, ln)
}

func waitForPeerState(t *testing.T, label string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}

func TestTCPMultipleBootstrapPeersServeBlocksAndReconnect(t *testing.T) {
	genesis := "shared-genesis-hash"
	local := NewServer(0, 198282, func() uint64 { return 3 })
	local.SetGenesisHash(genesis)
	local.SetNodeMode("full")
	listenPeerTestServer(t, local)

	primary := NewServer(0, 198282, func() uint64 { return 7 })
	primary.SetGenesisHash(genesis)
	primary.SetNodeMode("genesis")
	primaryAddr := listenPeerTestServer(t, primary)

	secondary := NewServer(0, 198282, func() uint64 { return 12 })
	secondary.SetGenesisHash(genesis)
	secondary.SetNodeMode("full")
	secondaryAddr := listenPeerTestServer(t, secondary)

	requests := make(chan GetBlocksPayload, 1)
	blockBatch := json.RawMessage(`["block-one","block-two"]`)
	primary.SetBlockProvider(func(from uint64, count int) json.RawMessage {
		requests <- GetBlocksPayload{From: from, Count: count}
		return blockBatch
	})
	received := make(chan json.RawMessage, 2)
	local.OnMessage(func(_ *Peer, msg Message) {
		if msg.Type == MsgBlocks {
			received <- msg.Payload
		}
	})

	if err := local.ConnectTo("tcp://" + primaryAddr); err != nil {
		t.Fatalf("connect to primary bootstrap: %v", err)
	}
	if err := local.ConnectTo(secondaryAddr); err != nil {
		t.Fatalf("connect to secondary bootstrap: %v", err)
	}
	waitForPeerState(t, "both peers and their handshakes", func() bool {
		return local.PeerCount() == 2 && primary.PeerCount() == 1 &&
			secondary.PeerCount() == 1 && local.MaxPeerHeight() == 12
	})
	peers := local.Peers()
	if len(peers) != 2 {
		t.Fatalf("local peer list has %d entries, want two", len(peers))
	}
	foundGenesis := false
	for _, peer := range peers {
		if !peer.Authorized || peer.Height == 0 {
			t.Fatalf("peer handshake status not populated: %#v", peer)
		}
		if peer.NodeMode == "genesis" && peer.Height == 7 {
			foundGenesis = true
		}
	}
	if !foundGenesis {
		t.Fatalf("genesis bootstrap handshake missing from peer list: %#v", peers)
	}

	local.RequestBlocks(4, 50)
	select {
	case req := <-requests:
		if req.From != 4 || req.Count != 50 {
			t.Fatalf("block request = %#v, want from=4 count=50", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bootstrap peer did not receive the block request")
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, blockBatch) {
			t.Fatalf("received block payload %s, want %s", got, blockBatch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("block batch was not propagated back to the syncing peer")
	}

	local.Disconnect(primaryAddr)
	waitForPeerState(t, "primary disconnect cleanup", func() bool {
		return local.PeerCount() == 1 && primary.PeerCount() == 0
	})
	if err := local.ConnectTo(primaryAddr); err != nil {
		t.Fatalf("reconnect to primary bootstrap: %v", err)
	}
	waitForPeerState(t, "primary reconnect and handshake", func() bool {
		return local.PeerCount() == 2 && primary.PeerCount() == 1 && local.MaxPeerHeight() == 12
	})
}

func TestTCPBootstrapCanRecoverAfterBeingOffline(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}

	local := NewServer(0, 198282, func() uint64 { return 0 })
	local.SetGenesisHash("shared-genesis-hash")
	listenPeerTestServer(t, local)
	if err := local.ConnectTo(addr); err == nil {
		t.Fatal("connecting to an offline bootstrap peer unexpectedly succeeded")
	}
	if local.PeerCount() != 0 {
		t.Fatalf("offline bootstrap left %d live peers", local.PeerCount())
	}

	remote := NewServer(0, 198282, func() uint64 { return 4 })
	remote.SetGenesisHash("shared-genesis-hash")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("start bootstrap at its recovered address: %v", err)
	}
	servePeerTestServer(t, remote, ln)
	if err := local.ConnectTo(addr); err != nil {
		t.Fatalf("retry recovered bootstrap peer: %v", err)
	}
	waitForPeerState(t, "recovered bootstrap handshake", func() bool {
		return local.PeerCount() == 1 && remote.PeerCount() == 1 && local.MaxPeerHeight() == 4
	})
}
