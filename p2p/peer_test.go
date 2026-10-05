package p2p

import (
	"encoding/json"
	"net"
	"testing"
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
