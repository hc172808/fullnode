package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
)

type options struct {
	dataDir, nodeKey, publicAddr, bootstrap, allowedNodes, genesisHash string
	configFile                                                         string
	p2pPort, maxPeers                                                  int
	chainID                                                            int64
	peerAuth                                                           bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bootnode:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.genesisHash == "" {
		opts.genesisHash, err = genesisHashForChainID(opts.chainID)
		if err != nil {
			return err
		}
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	node, err := p2p.NewBootnode(p2p.BootnodeConfig{
		DataDir:      opts.dataDir,
		NodeKeyPath:  opts.nodeKey,
		Port:         opts.p2pPort,
		MaxPeers:     opts.maxPeers,
		ChainID:      opts.chainID,
		GenesisHash:  opts.genesisHash,
		PublicAddr:   opts.publicAddr,
		Bootstrap:    splitList(opts.bootstrap),
		PeerAuth:     opts.peerAuth,
		AllowedNodes: splitList(opts.allowedNodes),
	})
	if err != nil {
		return err
	}
	if err := node.Start(); err != nil {
		return fmt.Errorf("start bootnode: %w", err)
	}
	defer node.Close()

	log.Info().
		Str("mode", "bootnode").
		Int64("chainId", opts.chainID).
		Str("genesisHash", opts.genesisHash).
		Int("p2pPort", node.P2PPort()).
		Int("maxPeers", opts.maxPeers).
		Str("nodeId", node.NodeID()).
		Msg("P2P discovery bootnode started (no RPC or block production)")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Info().Msg("Shutting down bootnode")
	return nil
}

func parseOptions(args []string) (options, error) {
	opts := options{
		dataDir:      envOr("GYDS_DATA_DIR", "/var/lib/gyds-bootnode"),
		nodeKey:      os.Getenv("GYDS_NODE_KEY"),
		publicAddr:   os.Getenv("GYDS_P2P_ADVERTISE_HOST"),
		bootstrap:    os.Getenv("GYDS_BOOTSTRAP_NODES"),
		allowedNodes: os.Getenv("GYDS_ALLOWED_NODES"),
		genesisHash:  os.Getenv("GYDS_GENESIS_HASH"),
		p2pPort:      envInt("GYDS_P2P_PORT", 30303),
		maxPeers:     envInt("GYDS_MAX_PEERS", 100),
		chainID:      int64(envInt("GYDS_CHAIN_ID", 198282)),
		peerAuth:     envBool("GYDS_PEER_AUTH", false),
	}

	fs := flag.NewFlagSet("bootnode", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.dataDir, "datadir", opts.dataDir, "bootnode data directory")
	fs.IntVar(&opts.p2pPort, "p2pport", opts.p2pPort, "P2P TCP listen port")
	fs.IntVar(&opts.maxPeers, "maxpeers", opts.maxPeers, "maximum concurrent P2P peers")
	fs.StringVar(&opts.nodeKey, "node-key", opts.nodeKey, "persistent node-key file path (default: <datadir>/node.key)")
	fs.Int64Var(&opts.chainID, "chain-id", opts.chainID, "network chain ID")
	fs.StringVar(&opts.publicAddr, "public-addr", opts.publicAddr, "public advertised host or host:port")
	fs.StringVar(&opts.bootstrap, "bootstrap", opts.bootstrap, "comma-separated upstream peer addresses")
	fs.StringVar(&opts.genesisHash, "genesis-hash", opts.genesisHash, "expected genesis block hash (required for custom chain IDs)")
	fs.BoolVar(&opts.peerAuth, "peer-auth", opts.peerAuth, "require approved peer identities")
	fs.StringVar(&opts.allowedNodes, "allowed-nodes", opts.allowedNodes, "comma-separated approved node IDs for --peer-auth")
	fs.StringVar(&opts.configFile, "config", "", "optional bootnode TOML configuration file")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	changed := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { changed[f.Name] = true })
	configPath := opts.configFile
	if configPath == "" {
		candidate := filepath.Join(opts.dataDir, "bootnode.toml")
		if _, err := os.Stat(candidate); err == nil {
			configPath = candidate
		} else if _, err := os.Stat("bootnode.toml"); err == nil {
			configPath = "bootnode.toml"
		}
	}
	if configPath != "" {
		values, err := readTOMLConfig(configPath)
		if err != nil {
			return options{}, err
		}
		if err := applyTOMLConfig(&opts, values, changed); err != nil {
			return options{}, fmt.Errorf("invalid bootnode config: %w", err)
		}
	}
	if opts.dataDir == "" {
		return options{}, fmt.Errorf("--datadir must not be empty")
	}
	if opts.chainID < 1 {
		return options{}, fmt.Errorf("--chain-id must be positive")
	}
	return opts, nil
}

func readTOMLConfig(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bootnode config %s: %w", path, err)
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		line = stripTOMLComment(line)
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value", path, lineNo)
		}
		key = normalizeTOMLKey(key)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty configuration key", path, lineNo)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate configuration key %q", path, lineNo, key)
		}
		values[key] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read bootnode config %s: %w", path, err)
	}
	return values, nil
}

func stripTOMLComment(line string) string {
	quoted := false
	escaped := false
	for i, r := range line {
		switch {
		case r == '"' && !escaped:
			quoted = !quoted
		case r == '#' && !quoted:
			return strings.TrimSpace(line[:i])
		case r == '\\' && quoted:
			escaped = !escaped
			continue
		}
		escaped = false
	}
	return strings.TrimSpace(line)
}

func normalizeTOMLKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(key)
}

func tomlString(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		return raw[1 : len(raw)-1]
	}
	return raw
}

func applyTOMLConfig(opts *options, values map[string]string, cli map[string]bool) error {
	setString := func(flagName, key string, target *string) {
		if !cli[flagName] {
			if value, ok := values[key]; ok {
				*target = tomlString(value)
			}
		}
	}
	setString("datadir", "datadir", &opts.dataDir)
	setString("node-key", "nodekey", &opts.nodeKey)
	setString("public-addr", "publicaddr", &opts.publicAddr)
	setString("bootstrap", "bootstrap", &opts.bootstrap)
	setString("allowed-nodes", "allowednodes", &opts.allowedNodes)
	setString("genesis-hash", "genesishash", &opts.genesisHash)

	parseInt := func(key string, bits int) (int64, bool, error) {
		value, ok := values[key]
		if !ok {
			return 0, false, nil
		}
		parsed, err := strconv.ParseInt(tomlString(value), 10, bits)
		if err != nil {
			return 0, true, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return parsed, true, nil
	}
	if value, ok, err := parseInt("p2pport", 32); err != nil {
		return err
	} else if ok && !cli["p2pport"] {
		opts.p2pPort = int(value)
	}
	if value, ok, err := parseInt("maxpeers", 32); err != nil {
		return err
	} else if ok && !cli["maxpeers"] {
		opts.maxPeers = int(value)
	}
	if value, ok, err := parseInt("chainid", 64); err != nil {
		return err
	} else if ok && !cli["chain-id"] {
		opts.chainID = value
	}
	if value, ok := values["peerauth"]; ok {
		parsed, err := strconv.ParseBool(tomlString(value))
		if err != nil {
			return fmt.Errorf("peerauth must be true or false: %w", err)
		}
		if !cli["peer-auth"] {
			opts.peerAuth = parsed
		}
	}
	return nil
}

func genesisHashForChainID(chainID int64) (string, error) {
	switch chainID {
	case 198282:
		return "", fmt.Errorf("mainnet genesis hash is not verified in this repository; supply the approved --genesis-hash")
	case 198281:
		genesis, err := core.GenesisForNetwork("testnet")
		if err != nil {
			return "", err
		}
		return core.GenesisBlock(genesis).Hash, nil
	case 198284:
		genesis, err := core.GenesisForNetwork("testnet3b")
		if err != nil {
			return "", err
		}
		return core.GenesisBlock(genesis).Hash, nil
	case 31337:
		return core.GenesisBlock(core.GydsTestGenesis).Hash, nil
	default:
		return "", fmt.Errorf("unknown chain ID %d: supply --genesis-hash for this network", chainID)
	}
}

func splitList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	value = strings.Trim(value, "[]")
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = tomlString(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	return value
}
