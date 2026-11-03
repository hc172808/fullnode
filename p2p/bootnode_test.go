package p2p

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBootnodeKeepsIdentityAndDoesNotServeBlocks(t *testing.T) {
	dataDir := t.TempDir()
	port := freeTCPPort(t)
	cfg := BootnodeConfig{
		DataDir:     dataDir,
		Port:        port,
		MaxPeers:    8,
		ChainID:     198282,
		GenesisHash: "0xproduction-genesis",
	}
	bootnode, err := NewBootnode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstID := bootnode.NodeID()
	if firstID == "" {
		t.Fatal("bootnode did not load or create a persistent identity")
	}
	if err := bootnode.Start(); err != nil {
		t.Fatal(err)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		bootnode.Close()
		t.Fatalf("dial bootnode P2P listener: %v", err)
	}
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)
	var hello Message
	if err := decoder.Decode(&hello); err != nil {
		conn.Close()
		bootnode.Close()
		t.Fatalf("read bootnode handshake: %v", err)
	}
	if hello.Type != MsgHandshake {
		conn.Close()
		bootnode.Close()
		t.Fatalf("first bootnode message = %q, want handshake", hello.Type)
	}
	var info PeerInfo
	if err := json.Unmarshal(hello.Payload, &info); err != nil {
		conn.Close()
		bootnode.Close()
		t.Fatalf("decode bootnode handshake: %v", err)
	}
	if info.NodeMode != "bootnode" || info.ChainID != cfg.ChainID || info.GenesisHash != cfg.GenesisHash {
		conn.Close()
		bootnode.Close()
		t.Fatalf("unexpected bootnode handshake identity: %#v", info)
	}

	peerHello, _ := json.Marshal(PeerInfo{
		ChainID:     cfg.ChainID,
		GenesisHash: cfg.GenesisHash,
		NodeMode:    "full",
		Version:     "test",
	})
	if err := encoder.Encode(Message{Type: MsgHandshake, Payload: peerHello}); err != nil {
		conn.Close()
		bootnode.Close()
		t.Fatalf("send client handshake: %v", err)
	}
	blockReq, _ := json.Marshal(GetBlocksPayload{From: 1, Count: 1})
	if err := encoder.Encode(Message{Type: MsgGetBlocks, Payload: blockReq}); err != nil {
		conn.Close()
		bootnode.Close()
		t.Fatalf("send block request: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var unexpected Message
	if err := decoder.Decode(&unexpected); err == nil {
		conn.Close()
		bootnode.Close()
		t.Fatalf("discovery-only bootnode unexpectedly replied with %q", unexpected.Type)
	}
	_ = conn.Close()
	bootnode.Close()

	keyInfo, err := os.Stat(filepath.Join(dataDir, "node.key"))
	if err != nil {
		t.Fatalf("persistent node key missing: %v", err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("node key mode = %04o, want 0600", keyInfo.Mode().Perm())
	}
	restarted, err := NewBootnode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.NodeID(); got != firstID {
		t.Fatalf("node ID after restart = %s, want stable ID %s", got, firstID)
	}
}

func TestBootnodeReturnsOnlyCompatibleDiscoveredPeers(t *testing.T) {
	cfg := BootnodeConfig{
		DataDir:     t.TempDir(),
		Port:        freeTCPPort(t),
		MaxPeers:    8,
		ChainID:     198282,
		GenesisHash: "0xproduction-genesis",
	}
	bootnode, err := NewBootnode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootnode.Start(); err != nil {
		t.Fatal(err)
	}
	defer bootnode.Close()

	first := dialRawPeer(t, cfg.Port, PeerInfo{
		ChainID:     cfg.ChainID,
		GenesisHash: cfg.GenesisHash,
		P2PAddr:     "192.0.2.10:30303",
		NodeMode:    "full",
		NodeID:      "candidate-a",
	})
	defer first.Close()
	waitFor(t, time.Second, func() bool {
		return len(bootnode.server.discoveryPeers(nil)) == 1
	})

	second := dialRawPeer(t, cfg.Port, PeerInfo{
		ChainID:     cfg.ChainID,
		GenesisHash: cfg.GenesisHash,
		P2PAddr:     "192.0.2.11:30303",
		NodeMode:    "full",
		NodeID:      "candidate-b",
	})
	defer second.Close()

	peerHello, _ := json.Marshal(PeerInfo{
		ChainID:     cfg.ChainID,
		GenesisHash: cfg.GenesisHash,
		NodeMode:    "full",
		NodeID:      "requester",
	})
	encoder := json.NewEncoder(second)
	if err := encoder.Encode(Message{Type: MsgGetPeers, Payload: peerHello}); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	var response Message
	if err := json.NewDecoder(second).Decode(&response); err != nil {
		t.Fatalf("read peer discovery response: %v", err)
	}
	if response.Type != MsgPeers {
		t.Fatalf("response type = %q, want peers", response.Type)
	}
	var payload PeersPayload
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Peers) != 1 {
		t.Fatalf("discovery returned %d peers, want 1: %#v", len(payload.Peers), payload.Peers)
	}
	if payload.Peers[0].Addr != "192.0.2.10:30303" || payload.Peers[0].NodeID != "candidate-a" {
		t.Fatalf("unexpected discovered endpoint: %#v", payload.Peers[0])
	}
}

func dialRawPeer(t *testing.T, port int, info PeerInfo) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("dial P2P peer: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	var hello Message
	if err := json.NewDecoder(conn).Decode(&hello); err != nil {
		conn.Close()
		t.Fatalf("read P2P handshake: %v", err)
	}
	payload, err := json.Marshal(info)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(Message{Type: MsgHandshake, Payload: payload}); err != nil {
		conn.Close()
		t.Fatalf("send P2P handshake: %v", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true before timeout")
}
