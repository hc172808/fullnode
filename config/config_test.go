package config

import (
	"strings"
	"testing"
)

func TestNormalizeNodeMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty defaults to full", input: "   ", want: "full"},
		{name: "normalizes supported mode", input: "  TESTNODE ", want: "testnode"},
		{name: "accepts full", input: "full", want: "full"},
		{name: "rejects unsupported mode", input: "fulll", want: "fulll", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeNodeMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeNodeMode(%q) error = %v, wantErr %t", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeNodeMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.input) {
				t.Fatalf("error %q does not include the original mode %q", err, tt.input)
			}
		})
	}
}

func TestFromEnvNormalizesNodeModeBeforeApplyingTestnodePorts(t *testing.T) {
	t.Setenv("GYDS_NODE_MODE", " TESTNODE ")
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")

	cfg := FromEnv()
	if cfg.NodeMode != "testnode" {
		t.Fatalf("NodeMode = %q, want testnode", cfg.NodeMode)
	}
	if cfg.DashboardPort != 15000 || cfg.RPCPort != 18545 || cfg.P2PPort != 31337 {
		t.Fatalf("testnode ports = dashboard:%d rpc:%d p2p:%d, want 15000/18545/31337",
			cfg.DashboardPort, cfg.RPCPort, cfg.P2PPort)
	}
}

func TestDashboardEnabledDefaultsToTrueAndCanBeDisabled(t *testing.T) {
	t.Setenv("GYDS_DASHBOARD_ENABLED", "")
	if cfg := FromEnv(); !cfg.DashboardEnabled {
		t.Fatal("DashboardEnabled defaults to false, want true")
	}

	t.Setenv("GYDS_DASHBOARD_ENABLED", "false")
	if cfg := FromEnv(); cfg.DashboardEnabled {
		t.Fatal("DashboardEnabled = true after explicit false")
	}
}

func TestFromEnvSelectsIsolatedTestnetProfile(t *testing.T) {
	t.Setenv("GYDS_NETWORK", "testnet")
	t.Setenv("GYDS_CHAIN_ID", "198282")
	t.Setenv("GYDS_NETWORK_NAME", "stale mainnet name")
	t.Setenv("GYDS_DATA_DIR", "./data")
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")

	cfg := FromEnv()
	if cfg.Network != "testnet" {
		t.Fatalf("Network = %q, want testnet", cfg.Network)
	}
	if cfg.ChainID != 198281 {
		t.Fatalf("ChainID = %d, want 198281", cfg.ChainID)
	}
	if cfg.NetworkName != "GYDS Testnet" {
		t.Fatalf("NetworkName = %q, want GYDS Testnet", cfg.NetworkName)
	}
	if cfg.DataDir != "./data/testnet" {
		t.Fatalf("DataDir = %q, want isolated ./data/testnet", cfg.DataDir)
	}
}

func TestFromEnvSelectsIsolatedThreeBTestnetProfile(t *testing.T) {
	t.Setenv("GYDS_NETWORK", "testnet3b")
	t.Setenv("GYDS_CHAIN_ID", "198281")
	t.Setenv("GYDS_NETWORK_NAME", "stale testnet name")
	t.Setenv("GYDS_DATA_DIR", "./data")
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")

	cfg := FromEnv()
	if cfg.Network != "testnet3b" || cfg.ChainID != 198284 || cfg.NetworkName != "GYDS Testnet 3B" {
		t.Fatalf("unexpected 3B profile: network=%q chainId=%d name=%q", cfg.Network, cfg.ChainID, cfg.NetworkName)
	}
	if cfg.DataDir != "./data/testnet3b" {
		t.Fatalf("DataDir = %q, want isolated ./data/testnet3b", cfg.DataDir)
	}
}

func TestFromEnvSwitchesBackToMainnetDataDirectory(t *testing.T) {
	t.Setenv("GYDS_NETWORK", "mainnet")
	t.Setenv("GYDS_DATA_DIR", "/var/lib/gyds-fullnode-testnet")
	t.Setenv("GYDS_BOOTSTRAP_NODES", "")

	cfg := FromEnv()
	if cfg.ChainID != 198282 {
		t.Fatalf("ChainID = %d, want 198282", cfg.ChainID)
	}
	if cfg.DataDir != "/var/lib/gyds-fullnode" {
		t.Fatalf("DataDir = %q, want preserved mainnet directory", cfg.DataDir)
	}
}

func TestProfileDataDirSwitchesBetweenIsolatedNetworks(t *testing.T) {
	tests := []struct {
		network string
		dir     string
		want    string
	}{
		{network: "testnet3b", dir: "/var/lib/gyds-fullnode", want: "/var/lib/gyds-fullnode-testnet3b"},
		{network: "testnet3b", dir: "/var/lib/gyds-fullnode-testnet", want: "/var/lib/gyds-fullnode-testnet3b"},
		{network: "testnet", dir: "/var/lib/gyds-fullnode-testnet3b", want: "/var/lib/gyds-fullnode-testnet"},
		{network: "mainnet", dir: "/var/lib/gyds-fullnode-testnet3b", want: "/var/lib/gyds-fullnode"},
		{network: "testnet3b", dir: "./data/testnet", want: "./data/testnet3b"},
		{network: "mainnet", dir: "./data/testnet3b", want: "./data"},
	}
	for _, tt := range tests {
		t.Run(tt.network+"_"+tt.dir, func(t *testing.T) {
			if got := ProfileDataDir(tt.network, tt.dir); got != tt.want {
				t.Fatalf("ProfileDataDir(%q, %q) = %q, want %q", tt.network, tt.dir, got, tt.want)
			}
		})
	}
}
