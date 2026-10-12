package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestBootstrapPeersPersistAcrossConfigReload(t *testing.T) {
	dataDir := t.TempDir()
	want := []string{"seed.example.net:30303", "[2001:db8::1]:30303"}
	if err := SaveBootstrapNodes(dataDir, want); err != nil {
		t.Fatalf("save bootstrap peers: %v", err)
	}
	if got := LoadBootstrapNodes(dataDir); !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded peers = %#v, want %#v", got, want)
	}

	t.Setenv("GYDS_NETWORK", "mainnet")
	t.Setenv("GYDS_DATA_DIR", filepath.Clean(dataDir))
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")
	cfg := FromEnv()
	if !reflect.DeepEqual(cfg.P2PBootstrap, want) {
		t.Fatalf("reloaded config peers = %#v, want %#v", cfg.P2PBootstrap, want)
	}
}
