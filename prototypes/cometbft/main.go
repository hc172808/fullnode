package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/config"
	cmtCrypto "github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	cmtTypes "github.com/cometbft/cometbft/types"
)

func main() {
	home := flag.String("home", filepath.Join(os.TempDir(), "gyds-cometbft-prototype-9198281"), "isolated prototype home; must be under the system temp directory")
	flag.Parse()
	if err := run(*home); err != nil {
		fmt.Fprintln(os.Stderr, "CometBFT prototype:", err)
		os.Exit(1)
	}
}

func run(home string) error {
	home, err := filepath.Abs(home)
	if err != nil {
		return fmt.Errorf("resolve prototype home: %w", err)
	}
	if err := requireIsolatedHome(home); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		return fmt.Errorf("create prototype config directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		return fmt.Errorf("create prototype data directory: %w", err)
	}

	cfg := config.DefaultConfig().SetRoot(home)
	cfg.RPC.ListenAddress = "tcp://127.0.0.1:36657"
	cfg.P2P.ListenAddress = "tcp://127.0.0.1:36656"
	cfg.P2P.Seeds = ""
	cfg.P2P.PersistentPeers = ""
	cfg.P2P.PexReactor = false
	cfg.Consensus.CreateEmptyBlocks = true
	cfg.Instrumentation.Prometheus = false
	if err := cfg.ValidateBasic(); err != nil {
		return fmt.Errorf("validate isolated CometBFT config: %w", err)
	}
	config.WriteConfigFile(filepath.Join(home, "config", "config.toml"), cfg)

	pv := privval.LoadOrGenFilePV(cfg.PrivValidatorKeyFile(), cfg.PrivValidatorStateFile())
	validatorPubKey, err := pv.GetPubKey()
	if err != nil {
		return fmt.Errorf("read isolated validator public key: %w", err)
	}
	if err := ensureGenesis(cfg.GenesisFile(), validatorPubKey); err != nil {
		return err
	}
	nodeKey, err := p2p.LoadOrGenNodeKey(cfg.NodeKeyFile())
	if err != nil {
		return fmt.Errorf("load or create isolated node key: %w", err)
	}

	app, err := newApp(filepath.Join(home, "app", "state.json"))
	if err != nil {
		return err
	}
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	cometNode, err := node.NewNode(
		cfg,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(abci.Application(app)),
		node.DefaultGenesisDocProviderFunc(cfg),
		config.DefaultDBProvider,
		node.DefaultMetricsProvider(cfg.Instrumentation),
		logger,
	)
	if err != nil {
		return fmt.Errorf("create isolated CometBFT node: %w", err)
	}
	if err := cometNode.Start(); err != nil {
		return fmt.Errorf("start isolated CometBFT node: %w", err)
	}

	fmt.Printf("Isolated CometBFT prototype running: chain ID %s, 2,000,000,000 GYDS, RPC http://127.0.0.1:36657\n", ChainID)
	fmt.Println("Prototype-only: accepts zero-fee EIP-155 native transfers; do not connect it to live peers or use it for funds.")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
	signal.Stop(signals)
	if err := cometNode.Stop(); err != nil {
		return fmt.Errorf("stop isolated CometBFT node: %w", err)
	}
	cometNode.Wait()
	return nil
}

func requireIsolatedHome(home string) error {
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve system temp directory: %w", err)
	}
	rel, err := filepath.Rel(tempRoot, home)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("prototype home %q must be a dedicated subdirectory of %q", home, tempRoot)
	}
	return nil
}

func ensureGenesis(path string, validator cmtCrypto.PubKey) error {
	_, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		existing, err := cmtTypes.GenesisDocFromFile(path)
		if err != nil {
			return fmt.Errorf("read prototype genesis: %w", err)
		}
		if existing.ChainID != ChainID {
			return fmt.Errorf("existing prototype genesis has chain ID %q, want %s; use a separate empty temp home", existing.ChainID, ChainID)
		}
		var state GenesisState
		if err := json.Unmarshal(existing.AppState, &state); err != nil {
			return fmt.Errorf("decode existing prototype genesis app state: %w", err)
		}
		if err := validateGenesisState(state); err != nil {
			return fmt.Errorf("existing prototype genesis is incompatible: %w", err)
		}
		expected, _ := json.Marshal(defaultGenesisState())
		actual, _ := json.Marshal(state)
		if !bytes.Equal(expected, actual) {
			return fmt.Errorf("existing prototype allocations differ from the isolated 2B GYDS genesis; use a separate empty temp home")
		}
		if len(existing.Validators) != 1 || !bytes.Equal(existing.Validators[0].PubKey.Bytes(), validator.Bytes()) {
			return fmt.Errorf("existing prototype validator does not match its local validator key; use a separate empty temp home")
		}
		return nil
	case !os.IsNotExist(statErr):
		return fmt.Errorf("check prototype genesis file: %w", statErr)
	}

	doc, err := makeGenesisDoc(ChainID, validator, defaultGenesisState())
	if err != nil {
		return err
	}
	if err := doc.SaveAs(path); err != nil {
		return fmt.Errorf("write isolated prototype genesis: %w", err)
	}
	return nil
}
