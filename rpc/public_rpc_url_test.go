package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gydschain/fullnode/core"
)

func TestNetworkMetadataUsesConfiguredPublicRPCURLAndPreservesQuery(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	s.publicRPCURL = "https://app.netlifegy.com/api/rpc?network=mainnet"

	response := httptest.NewRecorder()
	s.handleNetworkMetadata(response, httptest.NewRequest(http.MethodGet, "https://node.example/gyds-network.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, body=%s", response.Code, response.Body.String())
	}
	var metadata struct {
		RPCURLs []string `json:"rpcUrls"`
		WSURLs  []string `json:"wsUrls"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
		t.Fatalf("decode network metadata: %v", err)
	}
	if len(metadata.RPCURLs) != 1 || metadata.RPCURLs[0] != s.publicRPCURL {
		t.Fatalf("rpcUrls = %#v, want exact configured URL %q", metadata.RPCURLs, s.publicRPCURL)
	}
	if len(metadata.WSURLs) != 1 || metadata.WSURLs[0] != "wss://app.netlifegy.com/api/ws?network=mainnet" {
		t.Fatalf("wsUrls = %#v, want path before preserved query", metadata.WSURLs)
	}
}

func TestConnectionInfoUsesConfiguredPublicRPCURL(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	s.publicRPCURL = "https://app.netlifegy.com/api/rpc?network=mainnet"

	info := s.buildConnectionInfo()
	if got := info["rpc_url"]; got != s.publicRPCURL {
		t.Fatalf("rpc_url = %v, want %q", got, s.publicRPCURL)
	}
	if got := info["ws_url"]; got != "wss://app.netlifegy.com/api/ws?network=mainnet" {
		t.Fatalf("ws_url = %v, want query-preserving WebSocket URL", got)
	}
}

func TestMainnetNetworkMetadataUsesConfiguredDefaultRPCWhenUnset(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	response := httptest.NewRecorder()
	s.handleNetworkMetadata(response, httptest.NewRequest(http.MethodGet, "https://node.example/gyds-network.json", nil))

	var metadata struct {
		RPCURLs []string `json:"rpcUrls"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
		t.Fatalf("decode network metadata: %v", err)
	}
	if len(metadata.RPCURLs) != 1 || metadata.RPCURLs[0] != canonicalRPCURL {
		t.Fatalf("rpcUrls = %#v, want default mainnet endpoint %q", metadata.RPCURLs, canonicalRPCURL)
	}
}
