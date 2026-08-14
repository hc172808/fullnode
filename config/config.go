package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
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

func FromEnv() *Config {
	cfg := DefaultConfig()

	if v := os.Getenv("GYDS_CHAIN_ID"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.ChainID = id
		}
	}
	if v := os.Getenv("GYDS_NODE_MODE"); v != "" {
		cfg.NodeMode = v
	}
	if v := os.Getenv("GYDS_NETWORK_NAME"); v != "" {
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
		cfg.DataDir = v
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
	if v := os.Getenv("GYDS_EXTERNAL_URL"); v != "" {
		cfg.ExternalURL = v
	} else if v := os.Getenv("REPLIT_DEV_DOMAIN"); v != "" {
		cfg.ExternalURL = "https://" + strings.TrimPrefix(v, "https://")
	}

	return cfg
}
