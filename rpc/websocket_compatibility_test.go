package rpc

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gydschain/fullnode/core"
)

func subscriberCount(s *Server) int {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	return len(s.subs)
}

func waitForSubscribers(t *testing.T, s *Server, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if subscriberCount(s) == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("subscriber count = %d, want %d", subscriberCount(s), count)
}

// /api/ws is the custom dashboard feed, NOT Ethereum eth_subscribe.
func TestWebSocketUpgradeBroadcastDisconnectAndReconnect(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	httpServer := httptest.NewServer(s.rpcRouter)
	defer httpServer.Close()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/ws"
	for i := 0; i < 2; i++ {
		conn, response, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("upgrade through RPC middleware: response=%v err=%v", response, err)
		}
		t.Cleanup(func() { conn.Close() })
		waitForSubscribers(t, s, 1)
		s.NotifyNewBlock(s.chain.Head())
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var message struct {
			Type string                 `json:"type"`
			Data map[string]interface{} `json:"data"`
		}
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type != "newBlock" || message.Data["hash"] != s.chain.Head().Hash {
			t.Fatalf("unexpected block event: %#v", message)
		}
		s.subsMu.RLock()
		var ch chan interface{}
		for _, sub := range s.subs {
			ch = sub.ch
		}
		s.subsMu.RUnlock()
		conn.Close()
		waitForSubscribers(t, s, 0)
		select {
		case _, open := <-ch:
			if open {
				t.Fatal("disconnected subscriber channel is still open")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("disconnected subscriber writer was not released")
		}
	}
}
