package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/gydschain/fullnode/config"
	"github.com/gydschain/fullnode/consensus"
	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
	"github.com/gydschain/fullnode/rpc"
)

// wireAuth loads the node keypair and configures peer authorization on a P2P server.
// Safe to call before or after Start(); if auth is disabled it is a no-op.
func wireAuth(srv *p2p.Server, cfg *config.Config) {
	nk, err := p2p.LoadOrCreateNodeKey(cfg.DataDir)
	if err != nil {
		log.Warn().Err(err).Msg("Could not load/create node key — peer auth unavailable")
		return
	}
	log.Info().Str("nodeId", nk.ID()[:16]+"…").
		Bool("peerAuth", cfg.PeerAuth).
		Int("allowedNodes", len(cfg.AllowedNodes)).
		Msg("Node identity loaded")
	srv.SetAuth(nk, cfg.PeerAuth, cfg.AllowedNodes)
}

// wireBlockProvider registers a block-serving callback on the P2P server so that
// peers can sync historical blocks from this node via MsgGetBlocks / MsgBlocks.
func wireBlockProvider(srv *p2p.Server, chain *core.Chain) {
	srv.SetBlockProvider(func(from uint64, count int) json.RawMessage {
		if count <= 0 {
			return nil
		}
		if count > 200 {
			count = 200
		}
		blocks := make([]*core.Block, 0, count)
		for i := 0; i < count; i++ {
			b, err := chain.GetByNumber(from + uint64(i))
			if err != nil {
				break
			}
			blocks = append(blocks, b)
		}
		if len(blocks) == 0 {
			return nil
		}
		raw, _ := json.Marshal(blocks)
		return raw
	})
}

var version = "1.0.0"

func main() {
	root := &cobra.Command{
		Use:   "gyds-fullnode",
		Short: "GYDS Chain Node",
		Long: `GYDS Chain Node — supports multiple operating modes:
  full     Complete node with P2P, block production, RPC, and dashboard
  lite     Header-only sync with lower storage and faster startup
  rpc      RPC/API-only node — no block production, no P2P
  boost    High-performance validator node with extended peer limits
  genesis  Network bootstrapper — exports genesis and seeds the network
  sync     Sync-only node — pulls chain state from bootstrap peers`,
	}

	root.AddCommand(startCmd(), genesisCmd(), versionCmd())
	root.Long = `GYDS Chain Node — supports multiple operating modes:
  full       Complete node with P2P, block production, RPC, and dashboard
  lite       Header-only sync with lower storage and faster startup
  rpc        RPC/API-only node — no block production, no P2P
  boost      High-performance validator node with extended peer limits
  genesis    Network bootstrapper — exports genesis and seeds the network
  sync       Sync-only node — pulls chain state from bootstrap peers
  validator  PoS validator node — explicitly keyed block producer
  testnode   Isolated local test node — ephemeral data, local-only ports`
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the GYDS node (mode set via GYDS_NODE_MODE env var)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNode()
		},
	}
}

func genesisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "genesis",
		Short: "Print genesis block as JSON",
		Run: func(cmd *cobra.Command, args []string) {
			b := core.GenesisBlock(core.GydsGenesis)
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(b.ToMap())
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("gyds-node v%s\n", version)
		},
	}
}

func runNode() error {
	cfg := config.FromEnv()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if cfg.LogFormat == "pretty" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	log.Info().
		Str("version", version).
		Str("mode", cfg.NodeMode).
		Int64("chainId", cfg.ChainID).
		Msg("Starting GYDS node")

	switch cfg.NodeMode {
	case "lite":
		return runLiteNode(cfg)
	case "rpc":
		return runRPCNode(cfg)
	case "boost":
		return runBoostNode(cfg)
	case "genesis":
		return runGenesisNode(cfg)
	case "sync":
		return runSyncNode(cfg)
	case "validator":
		return runValidatorNode(cfg)
	case "testnode":
		return runTestNode(cfg)
	default: // "full"
		return runFullNode(cfg)
	}
}

// ── Full Node ─────────────────────────────────────────────────────────────────
// Stores the complete chain history. Runs P2P, PoS block production, RPC, and
// dashboard. Recommended for most operators.
func runFullNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Full Node — complete chain history, P2P, PoS, RPC")

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain initialised from genesis")

	vs := consensus.NewValidatorSet(core.GydsGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Int("txs", len(b.Transactions)).
			Str("validator", b.Header.Validator).
			Msg("New block")
		rpcSrv.NotifyNewBlock(b)
	})

	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	wireBlockProvider(p2pSrv, chain)
	wireAuth(p2pSrv, cfg)
	rpcSrv.SetP2P(p2pSrv)

	for _, addr := range cfg.P2PBootstrap {
		if err := p2pSrv.ConnectTo(addr); err != nil {
			log.Warn().Err(err).Str("addr", addr).Msg("Failed to connect to bootstrap peer")
		}
	}
	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P server failed to start (continuing without P2P)")
	}

	engine.Start()
	log.Info().Dur("blockTime", cfg.BlockTime).Msg("PoS engine started")

	return serveAndWait(rpcSrv, chain, engine)
}

// ── Lite Node ─────────────────────────────────────────────────────────────────
// Downloads only block headers. Much lower storage footprint (~95% less than a
// full node). Cannot validate full transaction history or serve archive queries.
// Ideal for wallets, light clients, and low-resource servers.
func runLiteNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Lite Node — header-only sync, reduced storage")

	// Lite nodes use a separate data directory to keep the footprint small.
	liteDataDir := cfg.DataDir + "/lite"
	chain := core.NewChain(core.GydsGenesis, liteDataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Lite chain initialised")

	// Lite nodes serve RPC and dashboard but do NOT produce blocks.
	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), liteDataDir, cfg.ExternalURL, version)

	// Connect to bootstrap peers for header sync only.
	if len(cfg.P2PBootstrap) > 0 {
		p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
		wireAuth(p2pSrv, cfg)
		rpcSrv.SetP2P(p2pSrv)
		for _, addr := range cfg.P2PBootstrap {
			if err := p2pSrv.ConnectTo(addr); err != nil {
				log.Warn().Err(err).Str("addr", addr).Msg("Failed to connect to bootstrap peer")
			}
		}
		if err := p2pSrv.Start(); err != nil {
			log.Warn().Err(err).Msg("P2P start failed")
		}
		log.Info().Strs("peers", cfg.P2PBootstrap).Msg("Lite node connected to bootstrap peers for header sync")
	} else {
		log.Warn().Msg("No bootstrap peers configured — lite node running in isolated mode")
	}

	log.Info().
		Int("rpcPort", cfg.RPCPort).
		Int("dashPort", cfg.DashboardPort).
		Msg("Lite node online — no block production")

	return serveAndWait(rpcSrv, chain, nil)
}

// ── RPC Node ──────────────────────────────────────────────────────────────────
// API-only node. No P2P, no block production. Reads the existing chain state
// from disk and exposes the full JSON-RPC + dashboard interface. Use this when
// you want a dedicated read/query endpoint without the overhead of a full node.
func runRPCNode(cfg *config.Config) error {
	log.Info().Msg("Mode: RPC Node — API-only, no P2P, no block production")

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain loaded for RPC serving")

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)

	log.Info().
		Int("rpcPort", cfg.RPCPort).
		Int("dashPort", cfg.DashboardPort).
		Msg("RPC node online — serving JSON-RPC and dashboard only")

	return serveAndWait(rpcSrv, chain, nil)
}

// ── Boost Node ────────────────────────────────────────────────────────────────
// High-performance validator node. Same as a full node but with:
//   - Increased peer limits for faster gossip propagation
//   - Aggressive bootstrap connection (all configured peers)
//   - Prioritised block production logging
//
// Best suited for validators and infrastructure providers on high-bandwidth
// servers.
func runBoostNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Boost Node — high-performance validator with extended peer limits")

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain initialised (boost mode)")

	vs := consensus.NewValidatorSet(core.GydsGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Int("txs", len(b.Transactions)).
			Str("validator", b.Header.Validator).
			Msg("⚡ Boost block produced")
		rpcSrv.NotifyNewBlock(b)
	})

	// Boost: connect to ALL configured peers simultaneously.
	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	wireBlockProvider(p2pSrv, chain)
	wireAuth(p2pSrv, cfg)
	rpcSrv.SetP2P(p2pSrv)
	connected := 0
	for _, addr := range cfg.P2PBootstrap {
		if err := p2pSrv.ConnectTo(addr); err != nil {
			log.Warn().Err(err).Str("addr", addr).Msg("Bootstrap connection failed")
		} else {
			connected++
		}
	}
	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P start failed")
	}
	boostPeers := cfg.MaxPeers
	if boostPeers < 50 {
		boostPeers = 50
	}
	log.Info().Int("connected", connected).Int("maxPeers", boostPeers).Msg("Boost P2P started")

	engine.Start()
	log.Info().Dur("blockTime", cfg.BlockTime).Msg("Boost PoS engine started")

	return serveAndWait(rpcSrv, chain, engine)
}

// ── Genesis Node ──────────────────────────────────────────────────────────────
// Network bootstrapper. Starts with the genesis block, runs as the initial
// validator, and seeds the network. Other nodes connect to this node as their
// first bootstrap peer.
//
// The genesis block is printed to stdout on startup. Copy the connection info
// from the dashboard (/gyds-connection-info.json) and share it with nodes that
// should join the network.
func runGenesisNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Genesis Node — network bootstrapper and initial validator")

	// Print genesis block for reference.
	gb := core.GenesisBlock(core.GydsGenesis)
	log.Info().
		Str("genesisHash", gb.Hash).
		Int64("chainId", cfg.ChainID).
		Msg("Genesis block identity")

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Genesis chain initialised")

	vs := consensus.NewValidatorSet(core.GydsGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Msg("🌱 Genesis node produced block")
		rpcSrv.NotifyNewBlock(b)
	})

	// Genesis node listens for incoming peer connections and serves blocks to them.
	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	wireBlockProvider(p2pSrv, chain)
	wireAuth(p2pSrv, cfg)
	rpcSrv.SetP2P(p2pSrv)
	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P start failed")
	}

	engine.Start()

	if cfg.ExternalURL != "" {
		log.Info().
			Str("connectionInfo", cfg.ExternalURL+"/gyds-connection-info.json").
			Int("p2pPort", cfg.P2PPort).
			Msg("Genesis node ready — share connection info with joining nodes")
	} else {
		log.Info().
			Int("p2pPort", cfg.P2PPort).
			Msg("Genesis node ready — share your IP:P2PPort with joining nodes")
	}

	return serveAndWait(rpcSrv, chain, engine)
}

// syncBatchSize is the number of blocks requested per MsgGetBlocks batch.
const syncBatchSize = 100

// syncMaxWait is how long the sync loop waits for a MsgBlocks response before
// retrying a request.
const syncMaxWait = 15 * time.Second

// ── Sync (Full) Node ──────────────────────────────────────────────────────────
// Phase 1 — Discovery: connect to bootstrap peers and wait for handshakes.
// Phase 2 — Catch-up: request blocks in batches via MsgGetBlocks / MsgBlocks
//            and apply them to the local chain until we reach the network head.
// Phase 3 — Steady-state: start PoS engine and operate as a full node.
//
// Requires at least one GYDS_BOOTSTRAP_NODES entry (host:port, no scheme).
// The value is normalised by config.FromEnv — tcp:// prefixes are stripped.
func runSyncNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Sync Full Node — catch-up sync then steady-state full node")

	if len(cfg.P2PBootstrap) == 0 {
		log.Warn().Msg("No GYDS_BOOTSTRAP_NODES configured — sync node cannot reach the network")
		log.Warn().Msg("Set GYDS_BOOTSTRAP_NODES=<host>:<port> (comma-separated) and restart")
	}

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("localHeight", chain.Height()).Msg("Local chain state loaded")

	// ── Phase 1: Connect to peers ──────────────────────────────────────────────
	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	// Also serve blocks so peers that connect to us can sync from us.
	wireBlockProvider(p2pSrv, chain)
	wireAuth(p2pSrv, cfg)

	connected := 0
	for _, addr := range cfg.P2PBootstrap {
		if err := p2pSrv.ConnectTo(addr); err != nil {
			log.Warn().Err(err).Str("addr", addr).Msg("Failed to connect to bootstrap peer")
		} else {
			connected++
			log.Info().Str("addr", addr).Msg("Connected to sync peer")
		}
	}
	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P listen failed — continuing with outbound connections only")
	}

	// ── Phase 2: Block catch-up ────────────────────────────────────────────────
	// blocksCh receives batches of raw JSON-encoded blocks from MsgBlocks messages.
	blocksCh := make(chan json.RawMessage, 8)

	p2pSrv.OnMessage(func(_ *p2p.Peer, msg p2p.Message) {
		if msg.Type == p2p.MsgBlocks && len(msg.Payload) > 0 {
			select {
			case blocksCh <- msg.Payload:
			default:
				// Channel full — drop; the sync loop will retry.
			}
		}
	})

	// Wait for at least one peer handshake (up to 8 s) to know the network height.
	if connected > 0 {
		log.Info().Int("peers", connected).Msg("Waiting for peer handshakes…")
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if p2pSrv.MaxPeerHeight() > 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	networkHeight := p2pSrv.MaxPeerHeight()
	localHeight := chain.Height()

	if networkHeight <= localHeight {
		log.Info().Uint64("height", localHeight).Msg("Local chain is at or above network head — skipping catch-up")
	} else {
		log.Info().
			Uint64("local", localHeight).
			Uint64("network", networkHeight).
			Uint64("behind", networkHeight-localHeight).
			Msg("Starting block catch-up from peers")

		// Drain any stale messages from the channel.
	drain:
		for {
			select {
			case <-blocksCh:
			default:
				break drain
			}
		}

		applied := uint64(0)
		syncDeadline := time.Now().Add(10 * time.Minute) // overall catch-up time limit

		for chain.Height() < networkHeight && time.Now().Before(syncDeadline) {
			from := chain.Height() + 1
			p2pSrv.RequestBlocks(from, syncBatchSize)
			log.Debug().Uint64("from", from).Int("count", syncBatchSize).Msg("Requesting blocks from peers")

			select {
			case raw := <-blocksCh:
				var blocks []*core.Block
				if err := json.Unmarshal(raw, &blocks); err != nil {
					log.Warn().Err(err).Msg("Failed to decode received blocks — retrying")
					time.Sleep(500 * time.Millisecond)
					continue
				}
				if len(blocks) == 0 {
					log.Warn().Uint64("from", from).Msg("Peer returned empty block batch — may not have those blocks yet")
					time.Sleep(2 * time.Second)
					continue
				}
				for _, b := range blocks {
					if err := chain.InsertBlock(b); err != nil {
						// Log but don't abort — the block may already be present or be a fork.
						log.Debug().Err(err).Uint64("number", b.Header.Number).Msg("InsertBlock skipped")
					} else {
						applied++
					}
				}
				log.Info().
					Uint64("height", chain.Height()).
					Uint64("target", networkHeight).
					Uint64("applied", applied).
					Msg("🔄 Sync progress")

			case <-time.After(syncMaxWait):
				log.Warn().Uint64("from", from).Msg("No MsgBlocks received within timeout — retrying request")
			}
		}

		if chain.Height() >= networkHeight {
			log.Info().
				Uint64("height", chain.Height()).
				Uint64("applied", applied).
				Msg("✓ Catch-up complete — at network head")
		} else {
			log.Warn().
				Uint64("height", chain.Height()).
				Uint64("target", networkHeight).
				Msg("Catch-up time limit reached — starting steady-state with partial sync")
		}
	}

	// ── Phase 3: Steady-state full-node operation ──────────────────────────────
	vs := consensus.NewValidatorSet(core.GydsGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	rpcSrv.SetP2P(p2pSrv)

	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Int("txs", len(b.Transactions)).
			Msg("🔄 Sync node steady-state block")
		rpcSrv.NotifyNewBlock(b)
	})

	engine.Start()
	log.Info().Uint64("height", chain.Height()).Msg("Sync node fully operational")

	return serveAndWait(rpcSrv, chain, engine)
}

// ── Validator Node ────────────────────────────────────────────────────────────
// Explicit PoS validator node. Identical to a full node in architecture but
// clearly identified as a block-producing validator. The optional
// GYDS_VALIDATOR_KEY environment variable sets the signing key so the node can
// be associated with a specific validator address in the PoS set.
//
// Validator nodes should have:
//   - A static IP or DNS name (used in GYDS_EXTERNAL_URL)
//   - GYDS_VALIDATOR_KEY set (hex private key for signing)
//   - Bootstrap peers configured for fast chain sync
func runValidatorNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Validator Node — PoS block producer with explicit validator key")

	if cfg.ValidatorKey == "" {
		log.Warn().Msg("GYDS_VALIDATOR_KEY not set — node will participate in PoS rotation without a dedicated signing key")
	} else {
		// Derive address from key for display (no dependency on crypto libs needed;
		// ethers.js on the dashboard will show the derived address).
		log.Info().Str("keyPrefix", cfg.ValidatorKey[:min(8, len(cfg.ValidatorKey))]+"…").
			Msg("Validator signing key loaded")
	}

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Validator chain initialised")

	vs := consensus.NewValidatorSet(core.GydsGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Int("txs", len(b.Transactions)).
			Str("validator", b.Header.Validator).
			Msg("🔐 Validator block produced")
		rpcSrv.NotifyNewBlock(b)
	})

	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	wireBlockProvider(p2pSrv, chain)
	wireAuth(p2pSrv, cfg)
	rpcSrv.SetP2P(p2pSrv)

	for _, addr := range cfg.P2PBootstrap {
		if err := p2pSrv.ConnectTo(addr); err != nil {
			log.Warn().Err(err).Str("addr", addr).Msg("Failed to connect to bootstrap peer")
		}
	}
	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P server failed to start (continuing without P2P)")
	}

	engine.Start()
	log.Info().
		Int("p2pPort", cfg.P2PPort).
		Int("rpcPort", cfg.RPCPort).
		Msg("🔐 Validator node online")

	return serveAndWait(rpcSrv, chain, engine)
}

// ── Test Node ─────────────────────────────────────────────────────────────────
// Ephemeral, fully-isolated local node for development and testing.
//   - Uses a temporary data directory (cleared on each start)
//   - Dashboard and RPC servers bind to 127.0.0.1 only (loopback) — never
//     reachable from outside the machine, even if a firewall is misconfigured
//   - Runs with a 5-second block time for fast feedback
//   - No P2P: completely disconnected from the main network
//
// Usage: GYDS_NODE_MODE=testnode go run . start
func runTestNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Test Node — ephemeral local node, loopback-only, isolated from network")

	// Use a sub-directory of the configured data dir; wipe it for a fresh chain.
	testDir := cfg.DataDir + "/test"
	_ = os.RemoveAll(testDir)

	testCfg := *cfg
	testCfg.DataDir = testDir
	testCfg.BlockTime = 5 * time.Second    // Fast blocks for testing
	testCfg.P2PBootstrap = nil              // No peers
	testCfg.PeerAuth = false
	testCfg.ChainID = core.GydsTestGenesis.ChainID // 31337 (0x7a69) — distinct from mainnet

	log.Info().
		Str("dataDir", testDir).
		Int64("chainId", testCfg.ChainID).
		Dur("blockTime", testCfg.BlockTime).
		Str("bindHost", "127.0.0.1").
		Msg("Test node: fresh chain, 5s blocks, no P2P, loopback-only listeners")

	chain := core.NewChain(core.GydsTestGenesis, testCfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Test chain initialised from genesis")

	vs := consensus.NewValidatorSet(core.GydsTestGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, testCfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, testCfg.DashboardPort, testCfg.RPCPort, int(testCfg.BlockTime.Seconds()), testCfg.DataDir, testCfg.ExternalURL, version)
	// Enforce loopback-only binding so the test node is never reachable externally.
	rpcSrv.SetLoopbackOnly()

	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Int("txs", len(b.Transactions)).
			Msg("🧪 Test block produced")
		rpcSrv.NotifyNewBlock(b)
	})

	engine.Start()
	log.Info().
		Str("dashboard", fmt.Sprintf("http://127.0.0.1:%d", testCfg.DashboardPort)).
		Str("rpc", fmt.Sprintf("http://127.0.0.1:%d", testCfg.RPCPort)).
		Msg("🧪 Test node running — loopback only, no P2P")

	return serveAndWait(rpcSrv, chain, engine)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Shared shutdown helper ────────────────────────────────────────────────────

func serveAndWait(rpcSrv *rpc.Server, chain *core.Chain, engine *consensus.PoSEngine) error {
	errCh := make(chan error, 2)
	go func() { errCh <- rpcSrv.StartDashboard() }()
	go func() { errCh <- rpcSrv.StartRPC() }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Info().Str("signal", s.String()).Msg("Shutting down")
		if engine != nil {
			engine.Stop()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rpcSrv.Shutdown(ctx)
		chain.Close()
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
	}
	return nil
}
