package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootnodeFlagsOverrideTOMLAndTomlIsRead(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "bootnode.toml")
	config := `data_dir = "/var/lib/gyds-bootnode"
p2p_port = 30304
max_peers = 40
node_key = "/var/lib/gyds-bootnode/identity.key"
chain_id = 198281
public_addr = "bootnode.example.net:30304"
bootstrap = ["peer-a.example.net:30303", "peer-b.example.net:30303"]
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, err := parseOptions([]string{"--config", configPath, "--p2pport", "30305"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.dataDir != "/var/lib/gyds-bootnode" ||
		opts.p2pPort != 30305 ||
		opts.maxPeers != 40 ||
		opts.nodeKey != "/var/lib/gyds-bootnode/identity.key" ||
		opts.chainID != 198281 ||
		opts.publicAddr != "bootnode.example.net:30304" {
		t.Fatalf("unexpected config values/CLI override: %#v", opts)
	}
	if got := splitList(opts.bootstrap); len(got) != 2 ||
		got[0] != "peer-a.example.net:30303" || got[1] != "peer-b.example.net:30303" {
		t.Fatalf("bootstrap list = %#v", got)
	}
}

func TestGenesisHashRequiresExplicitValueForUnknownChain(t *testing.T) {
	if _, err := genesisHashForChainID(55555); err == nil {
		t.Fatal("unknown network accepted without an explicit genesis hash")
	}
	if _, err := genesisHashForChainID(198282); err == nil {
		t.Fatal("mainnet genesis hash was inferred from the outdated local genesis")
	}
}

func TestReadTOMLConfigRejectsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootnode.toml")
	if err := os.WriteFile(path, []byte("not-a-setting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTOMLConfig(path); err == nil {
		t.Fatal("malformed TOML line accepted")
	}
}

func TestParseOptionsRejectsMalformedTOMLValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootnode.toml")
	if err := os.WriteFile(path, []byte("p2p_port = not-a-port\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseOptions([]string{"--config", path}); err == nil {
		t.Fatal("malformed TOML integer was silently ignored")
	}
}
