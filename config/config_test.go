package config

import "testing"

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