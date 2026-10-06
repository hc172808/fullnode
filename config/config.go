package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Network     string
	ChainID     int64
	NetworkName string
	NodeMode    string

	P2PPort          int
	P2PAdvertiseHost string
	P2PBootstrap     []string
	MaxPeers         int

	RPCPort       int
	RPCHost       string
	RPCEnabled    bool
	DashboardPort int

	WSPort    int
	WSEnabled bool

	DataDir     string
	LogLevel    string
	LogFormat   string
	ExternalURL string

	BlockTime time.Duration

	SyncMode     string
	SnapshotSync bool

	ValidatorKey string // hex private key for PoS validator signing

	// P2P peer authorization
	PeerAuth     bool     // if true, only AllowedNodes may connect
	AllowedNodes []string // whitelist of peer Node IDs (hex ed25519 public keys)
}

func DefaultConfig() *Config {
	return &Config{
		Network:          "mainnet",
		ChainID:          198282,
		NetworkName:      "GYDS Chain",
		NodeMode:         "full",
		P2PPort:          30303,
		P2PAdvertiseHost: "",
		P2PBootstrap:     []string{},
		MaxPeers:         25,
		RPCPort:          8545,
		DashboardPort:    5000,
		RPCHost:          "0.0.0.0",
		RPCEnabled:       true,
		WSPort:           8546,
		WSEnabled:        true,
		DataDir:          "./data",
		LogLevel:         "info",
		LogFormat:        "pretty",
		BlockTime:        120 * time.Second,
		SyncMode:         "full",
		SnapshotSync:     true,
	}
}

// PeerAuthRequired reports whether this node role exposes P2P connections.
// P2P-capable roles always require an authenticated, explicitly approved peer.
func PeerAuthRequired(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "rpc", "testnode":
		return false
	default:
		return true
	}
}

func FromEnv() *Config {
	cfg := DefaultConfig()

	networkConfigured := strings.TrimSpace(os.Getenv("GYDS_NETWORK")) != ""
	if networkConfigured {
		cfg.Network = strings.ToLower(strings.TrimSpace(os.Getenv("GYDS_NETWORK")))
		switch cfg.Network {
		case "mainnet":
			cfg.ChainID = 198282
			cfg.NetworkName = "GYDS Chain"
		case "testnet":
			cfg.ChainID = 198281
			cfg.NetworkName = "GYDS Testnet"
			cfg.DataDir = "./data/testnet"
		}
	} else if v := os.Getenv("GYDS_CHAIN_ID"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.ChainID = id
		}
	}
	if v := os.Getenv("GYDS_NODE_MODE"); v != "" {
		cfg.NodeMode = v
	}
	// The test node is intentionally isolated and uses its own standard ports
	// unless the operator explicitly overrides them.
	if strings.EqualFold(cfg.NodeMode, "testnode") {
		cfg.DashboardPort = 15000
		cfg.RPCPort = 18545
		cfg.WSPort = 18546
		cfg.P2PPort = 31337
	}
	if v := os.Getenv("GYDS_NETWORK_NAME"); v != "" && !networkConfigured {
		cfg.NetworkName = v
	}
	if v := os.Getenv("GYDS_P2P_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.P2PPort = p
		}
	}
	if v := os.Getenv("GYDS_P2P_ADVERTISE_HOST"); v != "" {
		cfg.P2PAdvertiseHost = strings.TrimSpace(v)
	}
	if v := os.Getenv("GYDS_RPC_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.RPCPort = p
		}
	}
	if v := os.Getenv("GYDS_DASHBOARD_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.DashboardPort = p
		}
	}
	if v := os.Getenv("GYDS_WS_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.WSPort = p
		}
	}
	if v := os.Getenv("GYDS_MAX_PEERS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.MaxPeers = p
		}
	}
	if v := os.Getenv("GYDS_RPC_HOST"); v != "" {
		cfg.RPCHost = v
	}
	if v := os.Getenv("GYDS_DATA_DIR"); v != "" {
		dataDir := strings.TrimSpace(v)
		if networkConfigured {
			cfg.DataDir = ProfileDataDir(cfg.Network, dataDir)
		} else {
			cfg.DataDir = dataDir
		}
	}
	if v := os.Getenv("GYDS_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("GYDS_LOG_FORMAT"); v != "" {
		cfg.LogFormat = v
	}
	if v := os.Getenv("GYDS_BOOTSTRAP_NODES"); v != "" {
		// Support comma-separated list of peers.
		// Strip the optional tcp:// scheme before storing so addresses are
		// safe to pass directly to net.Dial.
		for _, raw := range strings.Split(v, ",") {
			addr := strings.TrimSpace(raw)
			addr = strings.TrimPrefix(addr, "tcp://")
			addr = strings.TrimPrefix(addr, "TCP://")
			if addr != "" {
				cfg.P2PBootstrap = append(cfg.P2PBootstrap, addr)
			}
		}
	}
	// The admin panel also maintains a durable runtime copy. Merge it with
	// environment configuration so a restart or Git update cannot lose peers.
	cfg.P2PBootstrap = append(cfg.P2PBootstrap, LoadBootstrapNodes(cfg.DataDir)...)
	seenPeers := make(map[string]bool, len(cfg.P2PBootstrap))
	uniquePeers := cfg.P2PBootstrap[:0]
	for _, peer := range cfg.P2PBootstrap {
		peer = strings.TrimSpace(peer)
		if peer != "" && !seenPeers[peer] {
			seenPeers[peer] = true
			uniquePeers = append(uniquePeers, peer)
		}
	}
	cfg.P2PBootstrap = uniquePeers
	if v := os.Getenv("GYDS_BLOCK_TIME"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			cfg.BlockTime = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv("GYDS_VALIDATOR_KEY"); v != "" {
		cfg.ValidatorKey = v
	}
	if v := os.Getenv("GYDS_PEER_AUTH"); v == "true" || v == "1" || v == "yes" {
		cfg.PeerAuth = true
	}
	if v := os.Getenv("GYDS_ALLOWED_NODES"); v != "" {
		for _, raw := range strings.Split(v, ",") {
			if id := strings.TrimSpace(raw); id != "" {
				cfg.AllowedNodes = append(cfg.AllowedNodes, id)
			}
		}
	}
	if PeerAuthRequired(cfg.NodeMode) {
		// Do not let a stale GYDS_PEER_AUTH=false setting disable approval checks
		// on a P2P node. The allowlist itself may be empty, which denies all
		// inbound peer connections until the operator approves node IDs.
		cfg.PeerAuth = true
	}
	if v := os.Getenv("GYDS_EXTERNAL_URL"); v != "" {
		cfg.ExternalURL = v
	} else if v := os.Getenv("REPLIT_DEV_DOMAIN"); v != "" {
		cfg.ExternalURL = "https://" + strings.TrimPrefix(v, "https://")
	}

	return cfg
}

// ProfileDataDir maps a configured base directory to an isolated profile
// directory, and maps the testnet suffix back when returning to mainnet.
func ProfileDataDir(network, configuredDir string) string {
	cleanDir := filepath.Clean(strings.TrimSpace(configuredDir))
	switch network {
	case "testnet":
		switch cleanDir {
		case ".", "data", "/var/lib/gyds-fullnode":
			if filepath.IsAbs(cleanDir) {
				return "/var/lib/gyds-fullnode-testnet"
			}
			return "./data/testnet"
		}
		if strings.HasSuffix(cleanDir, "/testnet") || strings.HasSuffix(cleanDir, "-testnet") {
			return cleanDir
		}
		return filepath.Join(cleanDir, "testnet")
	case "mainnet":
		if strings.HasSuffix(cleanDir, "/testnet") {
			mainnetDir := strings.TrimSuffix(cleanDir, "/testnet")
			if mainnetDir == "data" {
				return "./data"
			}
			return mainnetDir
		}
		if strings.HasSuffix(cleanDir, "-testnet") {
			return strings.TrimSuffix(cleanDir, "-testnet")
		}
	}
	return configuredDir
}
