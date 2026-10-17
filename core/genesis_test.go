package core

import (
	"math/big"
	"testing"
)

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

func TestGenesisForNetworkBuildsSeparateThreeBTestnet(t *testing.T) {
	mainnet := GydsGenesis
	existingTestnet, err := GenesisForNetwork("testnet")
	if err != nil {
		t.Fatal(err)
	}
	testnet3b, err := GenesisForNetwork("testnet3b")
	if err != nil {
		t.Fatal(err)
	}
	if testnet3b == GydsGenesis || testnet3b == GydsTestGenesis || testnet3b == existingTestnet {
		t.Fatal("3B testnet must use a separate genesis configuration")
	}
	if testnet3b.ChainID != 198284 || testnet3b.NetworkName != "GYDS Testnet 3B" {
		t.Fatalf("unexpected 3B testnet identity: chainId=%d name=%q", testnet3b.ChainID, testnet3b.NetworkName)
	}
	if GenesisBlock(testnet3b).Hash == GenesisBlock(mainnet).Hash ||
		GenesisBlock(testnet3b).Hash == GenesisBlock(existingTestnet).Hash {
		t.Fatal("3B testnet must have a unique genesis hash")
	}
	wantBalances := []*big.Int{e18M(1500), e18M(900), e18M(600)}
	total := new(big.Int)
	if len(testnet3b.Alloc) != len(wantBalances) {
		t.Fatalf("3B testnet has %d native allocations, want %d", len(testnet3b.Alloc), len(wantBalances))
	}
	for i, want := range wantBalances {
		if testnet3b.Alloc[i].Balance.Cmp(want) != 0 {
			t.Errorf("native allocation %d = %s, want %s", i, testnet3b.Alloc[i].Balance, want)
		}
		total.Add(total, testnet3b.Alloc[i].Balance)
	}
	if total.Cmp(e18B(3)) != 0 {
		t.Fatalf("native genesis supply = %s, want 3B GYDS (%s)", total, e18B(3))
	}
	if len(testnet3b.Tokens) != 1 || testnet3b.Tokens[0].Symbol != "GYD" {
		t.Fatalf("unexpected 3B testnet token definitions: %+v", testnet3b.Tokens)
	}
	if testnet3b.Tokens[0].TotalSupply == nil || testnet3b.Tokens[0].TotalSupply.Sign() != 0 ||
		len(testnet3b.Tokens[0].Alloc) != 0 {
		t.Fatal("GYD must start with zero supply and no genesis allocations on the 3B testnet")
	}
	if mainnet.ChainID != 198282 || existingTestnet.ChainID != 198281 ||
		GydsGenesis.ChainID != 198282 || GydsTestGenesis.ChainID != 31337 {
		t.Fatal("selecting the 3B testnet mutated an existing network profile")
	}
	if len(GydsGenesis.Alloc) != 3 || GydsGenesis.Alloc[0].Balance.Cmp(e18M(500)) != 0 ||
		len(GydsTestGenesis.Tokens) == 0 || GydsTestGenesis.Tokens[0].TotalSupply.Cmp(e18B(10)) != 0 {
		t.Fatal("selecting the 3B testnet changed an existing network's genesis supply")
	}
}

func TestGenesisForNetworkRejectsUnknownProfile(t *testing.T) {
	if _, err := GenesisForNetwork("staging"); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}