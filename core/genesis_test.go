package core

import "testing"

func TestGenesisForNetworkKeepsMainnetAndIsolatesTestnet(t *testing.T) {
	mainnet, err := GenesisForNetwork("mainnet")
	if err != nil {
		t.Fatal(err)
	}
	if mainnet != GydsGenesis {
		t.Fatal("mainnet profile must retain the existing genesis configuration")
	}

	testnet, err := GenesisForNetwork("testnet")
	if err != nil {
		t.Fatal(err)
	}
	if testnet == GydsGenesis || testnet == GydsTestGenesis {
		t.Fatal("testnet profile must not mutate or reuse either shared genesis object")
	}
	if testnet.ChainID != 198281 || testnet.NetworkName != "GYDS Testnet" {
		t.Fatalf("unexpected testnet identity: chainId=%d name=%q", testnet.ChainID, testnet.NetworkName)
	}
	if GenesisBlock(testnet).Hash == GenesisBlock(GydsGenesis).Hash {
		t.Fatal("testnet and mainnet must have different genesis hashes")
	}
	if GydsGenesis.ChainID != 198282 || GydsTestGenesis.ChainID != 31337 {
		t.Fatal("selecting a profile mutated an existing genesis")
	}
}

func TestGenesisForNetworkRejectsUnknownProfile(t *testing.T) {
	if _, err := GenesisForNetwork("staging"); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}