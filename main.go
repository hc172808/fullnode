package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
// P2P-capable roles always require an approved node ID for inbound connections.
func wireAuth(srv *p2p.Server, cfg *config.Config) error {
	nk, err := p2p.LoadOrCreateNodeKey(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("load or create required P2P node identity: %w", err)
	}
	requireAuth := cfg.PeerAuth || config.PeerAuthRequired(cfg.NodeMode)
	log.Info().Str("nodeId", nk.ID()[:16]+"…").
		Bool("peerAuth", requireAuth).
		Int("allowedNodes", len(cfg.AllowedNodes)).
		Msg("Node identity loaded")
	srv.SetAuth(nk, requireAuth, cfg.AllowedNodes)
	return nil
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
	wireHeaderProvider(srv, chain)
}

func wireHeaderProvider(srv *p2p.Server, chain *core.Chain) {
	srv.SetHeaderProvider(func(from uint64, count int) json.RawMessage {
		if count <= 0 {
			return nil
		}
		if count > 200 {
			count = 200
		}
		headers := make([]*core.Header, 0, count)
		for i := 0; i < count; i++ {
			block, err := chain.GetByNumber(from + uint64(i))
			if err != nil {
				break
			}
			if block == nil || block.Header == nil {
				break
			}
			headers = append(headers, block.Header)
		}
		if len(headers) == 0 {
			return nil
		}
		raw, _ := json.Marshal(headers)
		return raw
	})
}

var version = "1.0.0"
var activeGenesis = core.GydsGenesis

func main() {
	root := &cobra.Command{
		Use:   "gyds-fullnode",
		Short: "GYDS Chain Node",
		Long: `GYDS Chain Node — supports multiple operating modes:
  full     Complete node with P2P, block production, RPC, and dashboard
  lite     Trusted-peer header sync only; no state proofs or block bodies
  rpc      RPC/API-only node — no block production, no P2P
  boost    High-performance validator node with extended peer limits
  genesis  Network bootstrapper — exports genesis and seeds the network
  sync     Sync-only node — pulls chain state from bootstrap peers`,
	}

	root.AddCommand(startCmd(), genesisCmd(), versionCmd())
	root.Long = `GYDS Chain Node — supports multiple operating modes:
  full       Complete node with P2P, block production, RPC, and dashboard
  lite       Trusted-peer header sync only; no state proofs or block bodies
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
		RunE: func(cmd *cobra.Command, args []string) error {
			genesis, err := core.GenesisForNetwork(config.FromEnv().Network)
			if err != nil {
				return err
			}
			b := core.GenesisBlock(genesis)
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(b.ToMap())
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
	if err := config.ValidatePublicRPCURL(cfg.PublicRPCURL); err != nil {
		return fmt.Errorf("GYDS_PUBLIC_RPC_URL: %w", err)
	}
	mode, err := config.NormalizeNodeMode(cfg.NodeMode)
	if err != nil {
		return err
	}
	cfg.NodeMode = mode
	approvedNodeIDs, revokedNodeIDs, err := rpc.LoadPeerAuthorizationIDs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("load approved peer registry: %w", err)
	}
	cfg.AllowedNodes = appendUniqueNodeIDs(cfg.AllowedNodes, approvedNodeIDs...)
	cfg.AllowedNodes = filterRevokedNodeIDs(cfg.AllowedNodes, revokedNodeIDs)

	genesis, err := core.GenesisForNetwork(cfg.Network)
	if err != nil {
		return err
	}
	activeGenesis = genesis
	cfg.ChainID = genesis.ChainID

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
	case "full":
		return runSyncNode(cfg)
	default:
		return fmt.Errorf("unsupported node mode %q", cfg.NodeMode)
	}
}

// ── Full Node ─────────────────────────────────────────────────────────────────
// Stores the complete chain history. Runs P2P, PoS block production, RPC, and
// dashboard. Recommended for most operators.
func runFullNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Full Node — complete chain history, P2P, PoS, RPC")

	chain := core.NewChain(activeGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain initialised from genesis")

	vs := consensus.NewValidatorSet(activeGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	rpcSrv.SetNodeMode(cfg.NodeMode)
	rpcSrv.SetDashboardEnabled(cfg.DashboardEnabled)
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
	p2pSrv.SetGenesisHash(core.GenesisBlock(activeGenesis).Hash)
	p2pSrv.SetMaxPeers(cfg.MaxPeers)
	p2pSrv.SetNodeMode(cfg.NodeMode)
	p2pSrv.SetAdvertiseHost(cfg.P2PAdvertiseHost)
	wireBlockProvider(p2pSrv, chain)
	if err := wireAuth(p2pSrv, cfg); err != nil {
		return err
	}
	rpcSrv.SetP2P(p2pSrv)
	rpcSrv.RequireSyncForServing(true)

	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P server failed to start (continuing without P2P)")
	}
	startBootstrapDialers(p2pSrv, cfg.P2PBootstrap)

	engine.Start()
	log.Info().Dur("blockTime", cfg.BlockTime).Msg("PoS engine started")

	return serveAndWait(rpcSrv, chain, engine)
}

// ── Lite Node ─────────────────────────────────────────────────────────────────
// Stores headers only and syncs from explicitly approved peers. Current block
// headers do not carry consensus finality proofs, so this mode is not trustless:
// the operator must trust the peers they authorize.
func runLiteNode(cfg *config.Config) error {
	if len(cfg.P2PBootstrap) == 0 {
		return fmt.Errorf("lite mode requires at least one trusted GYDS_BOOTSTRAP_NODES peer")
	}
	if len(cfg.AllowedNodes) == 0 {
		return fmt.Errorf("lite mode requires the bootstrap node IDs in GYDS_ALLOWED_NODES; peer authentication cannot be bypassed")
	}

	liteDataDir := cfg.DataDir + "/lite-headers"
	chain, err := core.NewHeaderChain(activeGenesis, liteDataDir)
	if err != nil {
		return fmt.Errorf("open lite header database: %w", err)
	}
	log.Info().Uint64("height", chain.Height()).Msg("Lite chain initialised")

	responses := make(chan liteHeaderResponse, 16)
	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	p2pSrv.SetGenesisHash(core.GenesisBlock(activeGenesis).Hash)
	p2pSrv.SetMaxPeers(cfg.MaxPeers)
	p2pSrv.SetNodeMode(cfg.NodeMode)
	p2pSrv.SetAdvertiseHost(cfg.P2PAdvertiseHost)
	wireHeaderProvider(p2pSrv, chain)
	if err := wireAuth(p2pSrv, cfg); err != nil {
		chain.Close()
		return err
	}
	p2pSrv.OnMessage(func(peer *p2p.Peer, msg p2p.Message) {
		if msg.Type != p2p.MsgHeaders || len(msg.Payload) == 0 {
			return
		}
		var response p2p.HeadersPayload
		if err := json.Unmarshal(msg.Payload, &response); err != nil {
			log.Warn().Err(err).Str("peer", peer.RemoteAddr()).Msg("invalid lite header response")
			return
		}
		select {
		case responses <- liteHeaderResponse{peerAddr: peer.RemoteAddr(), payload: response}:
		default:
			log.Warn().Str("peer", peer.RemoteAddr()).Msg("lite header response queue is full")
		}
	})
	if err := p2pSrv.Start(); err != nil {
		chain.Close()
		return fmt.Errorf("lite P2P start failed: %w", err)
	}
	startBootstrapDialers(p2pSrv, cfg.P2PBootstrap)

	// Lite mode intentionally exposes no dashboard: account and transaction
	// state is unavailable from headers alone.
	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), liteDataDir, cfg.ExternalURL, version)
	rpcSrv.SetNodeMode(cfg.NodeMode)
	rpcSrv.SetDashboardEnabled(false)
	if os.Getenv("GYDS_RPC_HOST") == "" {
		rpcSrv.SetLoopbackOnly()
	} else {
		rpcSrv.SetBindHost(cfg.RPCHost)
	}
	rpcSrv.SetP2P(p2pSrv)
	rpcSrv.RequireSyncForServing(true)
	go syncLiteHeaders(context.Background(), chain, p2pSrv, responses)

	log.Info().
		Int("rpcPort", cfg.RPCPort).
		Int("p2pPort", cfg.P2PPort).
		Strs("trustedPeers", cfg.P2PBootstrap).
		Msg("Lite node online — header-only, trusted peers, no state proofs or block production")

	return serveAndWait(rpcSrv, chain, nil)
}

type liteHeaderResponse struct {
	peerAddr string
	payload  p2p.HeadersPayload
}

const liteHeaderBatchSize = 200

func syncLiteHeaders(ctx context.Context, chain *core.Chain, peers *p2p.Server, responses <-chan liteHeaderResponse) {
	statusTicker := time.NewTicker(15 * time.Second)
	defer statusTicker.Stop()
	peers.RequestStatus()

	for {
		localHeight := chain.Height()
		networkHeight := peers.MaxPeerHeight()
		if peers.PeerCount() > 0 && networkHeight > localHeight {
			from := localHeight + 1
			count := int(networkHeight - localHeight)
			if count > liteHeaderBatchSize {
				count = liteHeaderBatchSize
			}
			peers.RequestHeaders(from, count)

			select {
			case response := <-responses:
				if response.payload.From != from {
					continue
				}
				var headers []*core.Header
				if err := json.Unmarshal(response.payload.Headers, &headers); err != nil ||
					len(headers) == 0 || len(headers) > count {
					log.Warn().Err(err).Str("peer", response.peerAddr).Msg("invalid lite header batch")
					peers.Disconnect(response.peerAddr)
					continue
				}
				for i, header := range headers {
					if header == nil || header.Number != from+uint64(i) {
						log.Warn().Str("peer", response.peerAddr).Uint64("from", from).Msg("non-contiguous lite header batch")
						peers.Disconnect(response.peerAddr)
						break
					}
					if err := chain.InsertHeader(header); err != nil {
						log.Warn().Err(err).Str("peer", response.peerAddr).Uint64("height", header.Number).Msg("rejected lite header")
						peers.Disconnect(response.peerAddr)
						break
					}
				}
				log.Info().
					Uint64("height", chain.Height()).
					Uint64("peerHeight", networkHeight).
					Str("peer", response.peerAddr).
					Msg("Lite header sync progress")
			case <-time.After(15 * time.Second):
				log.Warn().Uint64("from", from).Msg("timed out waiting for lite headers")
			case <-ctx.Done():
				return
			}
			continue
		}

		select {
		case <-statusTicker.C:
			peers.RequestStatus()
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

// ── RPC Node ──────────────────────────────────────────────────────────────────
// RPC node. Serves JSON-RPC and the dashboard without block production or P2P.
func runRPCNode(cfg *config.Config) error {
	log.Info().Msg("Mode: RPC Node — API-only, no P2P, no block production")

	chain := core.NewChain(activeGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain loaded for RPC serving")

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	rpcSrv.SetNodeMode(cfg.NodeMode)
	rpcSrv.SetDashboardEnabled(cfg.DashboardEnabled)
	rpcSrv.RequireSyncForServing(true)

	log.Info().
		Int("rpcPort", cfg.RPCPort).
		Int("dashPort", cfg.DashboardPort).
		Msg("RPC node online — serving JSON-RPC and dashboard without P2P")

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

	chain := core.NewChain(activeGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain initialised (boost mode)")

	vs := consensus.NewValidatorSet(activeGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	rpcSrv.SetNodeMode(cfg.NodeMode)
	rpcSrv.SetDashboardEnabled(cfg.DashboardEnabled)
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
	p2pSrv.SetGenesisHash(core.GenesisBlock(activeGenesis).Hash)
	p2pSrv.SetMaxPeers(cfg.MaxPeers)
	p2pSrv.SetNodeMode(cfg.NodeMode)
	p2pSrv.SetAdvertiseHost(cfg.P2PAdvertiseHost)
	wireBlockProvider(p2pSrv, chain)
	if err := wireAuth(p2pSrv, cfg); err != nil {
		return err
	}
	rpcSrv.SetP2P(p2pSrv)
	rpcSrv.RequireSyncForServing(true)
	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P start failed")
	}
	boostPeers := cfg.MaxPeers
	if boostPeers < 50 {
		boostPeers = 50
	}
	p2pSrv.SetMaxPeers(boostPeers)
	startBootstrapDialers(p2pSrv, cfg.P2PBootstrap)
	log.Info().Int("maxPeers", boostPeers).Msg("Boost P2P started")

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
	gb := core.GenesisBlock(activeGenesis)
	log.Info().
		Str("genesisHash", gb.Hash).
		Int64("chainId", cfg.ChainID).
		Msg("Genesis block identity")

	chain := core.NewChain(activeGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Genesis chain initialised")

	vs := consensus.NewValidatorSet(activeGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	rpcSrv.SetNodeMode(cfg.NodeMode)
	rpcSrv.SetDashboardEnabled(cfg.DashboardEnabled)
	engine.OnNewBlock(func(b *core.Block) {
		log.Info().
			Uint64("number", b.Header.Number).
			Str("hash", b.Hash[:16]+"...").
			Msg("🌱 Genesis node produced block")
		rpcSrv.NotifyNewBlock(b)
	})

	// Genesis node listens for incoming peer connections and serves blocks to them.
	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	p2pSrv.SetGenesisHash(core.GenesisBlock(activeGenesis).Hash)
	p2pSrv.SetMaxPeers(cfg.MaxPeers)
	p2pSrv.SetNodeMode(cfg.NodeMode)
	p2pSrv.SetAdvertiseHost(cfg.P2PAdvertiseHost)
	wireBlockProvider(p2pSrv, chain)
	if err := wireAuth(p2pSrv, cfg); err != nil {
		return err
	}
	rpcSrv.SetP2P(p2pSrv)
	rpcSrv.RequireSyncForServing(true)
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
//
//	and apply them to the local chain until we reach the network head.
//
// Phase 3 — Steady-state: start PoS engine and operate as a full node.
//
// Requires at least one GYDS_BOOTSTRAP_NODES entry (host:port, no scheme).
// The value is normalised by config.FromEnv — tcp:// prefixes are stripped.
func runSyncNode(cfg *config.Config) error {
	log.Info().Msg("Mode: Sync Full Node — catch-up sync then steady-state full node")

	if len(cfg.P2PBootstrap) == 0 {
		return fmt.Errorf("sync requires GYDS_BOOTSTRAP_NODES=<public-host>:<p2p-port>; no bootstrap peers are configured")
	}
	if len(cfg.AllowedNodes) == 0 {
		return fmt.Errorf("sync requires trusted peer IDs in GYDS_ALLOWED_NODES")
	}

	chain := core.NewChain(activeGenesis, cfg.DataDir)
	log.Info().Uint64("localHeight", chain.Height()).Msg("Local chain state loaded")

	// ── Phase 1: Connect to peers ──────────────────────────────────────────────
	p2pSrv := p2p.NewServer(cfg.P2PPort, cfg.ChainID, chain.Height)
	p2pSrv.SetGenesisHash(core.GenesisBlock(activeGenesis).Hash)
	p2pSrv.SetMaxPeers(cfg.MaxPeers)
	p2pSrv.SetNodeMode(cfg.NodeMode)
	p2pSrv.SetAdvertiseHost(cfg.P2PAdvertiseHost)
	// Also serve blocks so peers that connect to us can sync from us.
	wireBlockProvider(p2pSrv, chain)
	if err := wireAuth(p2pSrv, cfg); err != nil {
		return err
	}

	if err := p2pSrv.Start(); err != nil {
		return fmt.Errorf("sync P2P listener failed: %w", err)
	}
	defer p2pSrv.Close()

	rpcSrv := rpc.NewServer(chain, cfg.DashboardPort, cfg.RPCPort, int(cfg.BlockTime.Seconds()), cfg.DataDir, cfg.ExternalURL, version)
	rpcSrv.SetNodeMode(cfg.NodeMode)
	rpcSrv.SetDashboardEnabled(cfg.DashboardEnabled)
	rpcSrv.SetP2P(p2pSrv)
	rpcSrv.RequireSyncForServing(true)
	if _, err := rpcSrv.StartBackground(); err != nil {
		return fmt.Errorf("start sync progress endpoints: %w", err)
	}

	type bootstrapResult struct {
		addr string
		err  error
	}
	results := make(chan bootstrapResult, len(cfg.P2PBootstrap))
	for _, addr := range cfg.P2PBootstrap {
		go func(addr string) {
			results <- bootstrapResult{addr: addr, err: connectBootstrapWithRetry(p2pSrv, addr)}
		}(addr)
	}

	connected := 0
	remaining := len(cfg.P2PBootstrap)
	for connected == 0 && remaining > 0 {
		result := <-results
		remaining--
		if result.err != nil {
			log.Warn().Err(result.err).Str("addr", result.addr).Msg("Bootstrap peer unavailable after retries")
			continue
		}
		connected++
		log.Info().Str("addr", result.addr).Msg("Connected to sync peer")
	}
	if connected == 0 {
		return fmt.Errorf("sync could not connect to any bootstrap peer; verify GYDS_BOOTSTRAP_NODES uses a public host:port and TCP %d is open", cfg.P2PPort)
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

	// Wait for at least one peer handshake (up to 8 s). A peer at genesis has
	// height zero, so PeerCount—not MaxPeerHeight—is the connection signal.
	if connected > 0 {
		log.Info().Int("peers", connected).Msg("Waiting for peer handshakes…")
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if p2pSrv.PeerCount() > 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if p2pSrv.PeerCount() == 0 {
		return fmt.Errorf("bootstrap TCP connection succeeded but no peer handshake was accepted; check chain ID %d, peer authorization, and the remote node logs", cfg.ChainID)
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

		for time.Now().Before(syncDeadline) {
			if latestHeight := p2pSrv.MaxPeerHeight(); latestHeight > networkHeight {
				networkHeight = latestHeight
			}
			if chain.Height() >= networkHeight {
				break
			}
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

		if err := ensureSyncCaughtUp(chain.Height(), networkHeight); err != nil {
			log.Error().Err(err).Msg("Sync failed; refusing to start block production")
			return err
		}
		log.Info().
			Uint64("height", chain.Height()).
			Uint64("applied", applied).
			Msg("✓ Catch-up complete — at network head")
	}

	// ── Phase 3: Steady-state full-node operation ──────────────────────────────
	vs := consensus.NewValidatorSet(activeGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

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

func connectBootstrapWithRetry(srv *p2p.Server, addr string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		log.Info().Str("addr", addr).Int("attempt", attempt).Msg("Dialing bootstrap peer")
		if err := srv.ConnectTo(addr); err == nil {
			return nil
		} else {
			lastErr = err
			if attempt < 3 {
				time.Sleep(time.Duration(attempt*2) * time.Second)
			}
		}
	}
	return lastErr
}

func ensureSyncCaughtUp(localHeight, networkHeight uint64) error {
	if localHeight < networkHeight {
		return fmt.Errorf(
			"sync catch-up incomplete: local height %d is below peer height %d; refusing to start block production",
			localHeight,
			networkHeight,
		)
	}
	return nil
}

func appendUniqueNodeIDs(base []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(additions))
	result := make([]string, 0, len(base)+len(additions))
	for _, id := range append(append([]string(nil), base...), additions...) {
		id = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(id, "0x")))
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func filterRevokedNodeIDs(ids, revoked []string) []string {
	revokedSet := make(map[string]struct{}, len(revoked))
	for _, id := range revoked {
		id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "0x")
		revokedSet[id] = struct{}{}
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "0x")
		if _, isRevoked := revokedSet[id]; !isRevoked {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// startBootstrapDialers starts outbound dialing only after the P2P listener
// has been started. This keeps the dashboard and RPC available while a
// bootstrap host is slow or temporarily offline.
func startBootstrapDialers(srv *p2p.Server, addrs []string) {
	for _, addr := range addrs {
		go func(addr string) {
			bootstrapRetryLoop(
				context.Background(),
				addr,
				srv.IsConnectedTo,
				func(peerAddr string) error {
					return connectBootstrapWithRetry(srv, peerAddr)
				},
				time.Second,
				30*time.Second,
			)
		}(addr)
	}
}

// bootstrapRetryLoop keeps a configured peer alive and retries temporary
// startup/network failures with exponential backoff. The callbacks keep this
// loop directly testable without requiring external hosts.
func bootstrapRetryLoop(
	ctx context.Context,
	addr string,
	connected func(string) bool,
	connect func(string) error,
	initialDelay, maxDelay time.Duration,
) {
	if initialDelay <= 0 {
		initialDelay = time.Second
	}
	if maxDelay < initialDelay {
		maxDelay = initialDelay
	}
	delay := initialDelay
	for {
		if !connected(addr) {
			err := connect(addr)
			if err != nil {
				log.Warn().Err(err).Str("addr", addr).Dur("retryIn", delay).Msg("Bootstrap peer unavailable; retrying")
			} else if !waitForBootstrapConnection(ctx, addr, connected, 5*time.Second) {
				log.Warn().Str("addr", addr).Dur("retryIn", delay).Msg("Bootstrap TCP dial did not complete an authorized peer handshake; retrying")
			}
		} else {
			delay = initialDelay
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func waitForBootstrapConnection(ctx context.Context, addr string, connected func(string) bool, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if connected(addr) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return connected(addr)
		case <-ticker.C:
		}
	}
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
	}

	chain := core.NewChain(activeGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Validator chain initialised")

	vs := consensus.NewValidatorSet(activeGenesis.Validators)
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
	p2pSrv.SetGenesisHash(core.GenesisBlock(activeGenesis).Hash)
	p2pSrv.SetMaxPeers(cfg.MaxPeers)
	p2pSrv.SetNodeMode(cfg.NodeMode)
	p2pSrv.SetAdvertiseHost(cfg.P2PAdvertiseHost)
	wireBlockProvider(p2pSrv, chain)
	if err := wireAuth(p2pSrv, cfg); err != nil {
		return err
	}
	rpcSrv.SetP2P(p2pSrv)
	rpcSrv.RequireSyncForServing(true)

	if err := p2pSrv.Start(); err != nil {
		log.Warn().Err(err).Msg("P2P server failed to start (continuing without P2P)")
	}
	startBootstrapDialers(p2pSrv, cfg.P2PBootstrap)

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
	testCfg.BlockTime = 5 * time.Second // Fast blocks for testing
	testCfg.P2PBootstrap = nil          // No peers
	testCfg.PeerAuth = false
	testCfg.ChainID = core.GydsTestGenesis.ChainID // 31337 (0x7a69) — distinct from mainnet
	testGenesis := *core.GydsTestGenesis
	testGenesis.EVMActivationBlock = 1

	log.Info().
		Str("dataDir", testDir).
		Int64("chainId", testCfg.ChainID).
		Uint64("evmActivationBlock", testGenesis.EVMActivationBlock).
		Dur("blockTime", testCfg.BlockTime).
		Str("bindHost", "127.0.0.1").
		Msg("Test node: fresh chain, 5s blocks, no P2P, loopback-only listeners")

	chain := core.NewChain(&testGenesis, testCfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Test chain initialised from genesis")

	vs := consensus.NewValidatorSet(testGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, testCfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, testCfg.DashboardPort, testCfg.RPCPort, int(testCfg.BlockTime.Seconds()), testCfg.DataDir, testCfg.ExternalURL, version)
	rpcSrv.SetNodeMode(testCfg.NodeMode)
	rpcSrv.SetDashboardEnabled(testCfg.DashboardEnabled)
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
	errCh, err := rpcSrv.StartBackground()
	if err != nil {
		return err
	}
	if !rpcSrv.DashboardEnabled() {
		log.Info().Msg("Dashboard UI disabled; JSON-RPC remains available")
	}

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
