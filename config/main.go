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

var version = "1.0.0"

func main() {
	root := &cobra.Command{
		Use:   "gyds-fullnode",
		Short: "GYDS Chain Full Node",
		Long: `GYDS Fullnode — a full blockchain node for the GYDS Chain.
Supports full sync, RPC API, WebSocket subscriptions, and P2P networking.

Run 'gyds-fullnode setup' to open the web configuration wizard.`,
	}

	root.AddCommand(startCmd(), genesisCmd(), versionCmd(), setupCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func setupCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Launch the web setup wizard to configure this node",
		Long:  `Opens a local web server on port 8888 (by default) serving a step-by-step configuration wizard for the GYDS Full Node.`,
		Run: func(cmd *cobra.Command, args []string) {
			startFullSetupWizard(port, version)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8888, "Port for the setup wizard web server")
	return cmd
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the GYDS fullnode",
		Long:  `Start the GYDS fullnode. Run 'gyds-fullnode setup' first to configure it via web UI.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNode()
		},
	}
}

func genesisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "genesis",
		Short: "Print genesis block",
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
			fmt.Printf("gyds-fullnode v%s\n", version)
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
		Msg("Starting GYDS fullnode")

	chain := core.NewChain(core.GydsGenesis, cfg.DataDir)
	log.Info().Uint64("height", chain.Height()).Msg("Chain initialised from genesis")

	vs := consensus.NewValidatorSet(core.GydsGenesis.Validators)
	engine := consensus.NewPoSEngine(chain, vs, cfg.BlockTime)

	rpcSrv := rpc.NewServer(chain, cfg.RPCPort, int(cfg.BlockTime.Seconds()))
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- rpcSrv.Start()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sig:
		log.Info().Str("signal", s.String()).Msg("Shutting down")
		engine.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rpcSrv.Shutdown(ctx)
		chain.Close()
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("RPC server: %w", err)
		}
	}
	return nil
}
