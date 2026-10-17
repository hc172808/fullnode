package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	httpclient "github.com/cometbft/cometbft/rpc/client/http"
	cmtTypes "github.com/cometbft/cometbft/types"
)

const clusterValidatorCount = 3

type clusterMember struct {
	home    string
	cfg     *config.Config
	privVal *privval.FilePV
	nodeKey *p2p.NodeKey
	p2pPort int
	rpcPort int
	rpc     *httpclient.HTTP
}

func TestThreeValidatorClusterCommitsMatchingBlocks(t *testing.T) {
	root := t.TempDir()
	usedPorts := make(map[int]struct{}, clusterValidatorCount*2)
	members := make([]clusterMember, clusterValidatorCount)
	validators := make([]cmtTypes.GenesisValidator, clusterValidatorCount)

	for i := range members {
		home := filepath.Join(root, fmt.Sprintf("validator-%d", i+1))
		for _, dir := range []string{
			filepath.Join(home, "config"),
			filepath.Join(home, "data"),
		} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("create validator directory: %v", err)
			}
		}

		cfg := config.DefaultConfig().SetRoot(home)
		cfg.RPC.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", reserveClusterPort(t, usedPorts))
		members[i].p2pPort = reserveClusterPort(t, usedPorts)
		members[i].rpcPort = mustPortFromAddress(t, cfg.RPC.ListenAddress)
		cfg.P2P.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", members[i].p2pPort)
		cfg.P2P.AllowDuplicateIP = true
		cfg.P2P.PexReactor = false
		cfg.Consensus.CreateEmptyBlocks = true
		cfg.Consensus.TimeoutCommit = 500 * time.Millisecond
		cfg.Instrumentation.Prometheus = false

		privVal := privval.GenFilePV(cfg.PrivValidatorKeyFile(), cfg.PrivValidatorStateFile())
		privVal.Save()
		pubKey, err := privVal.GetPubKey()
		if err != nil {
			t.Fatalf("read validator %d public key: %v", i+1, err)
		}
		nodeKey, err := p2p.LoadOrGenNodeKey(cfg.NodeKeyFile())
		if err != nil {
			t.Fatalf("create validator %d P2P key: %v", i+1, err)
		}

		members[i].home = home
		members[i].cfg = cfg
		members[i].privVal = privVal
		members[i].nodeKey = nodeKey
		validators[i] = cmtTypes.GenesisValidator{
			Address: pubKey.Address(),
			PubKey:  pubKey,
			Power:   1,
			Name:    fmt.Sprintf("isolated-validator-%d", i+1),
		}
	}

	genesis := clusterGenesis(t, validators)
	for i := range members {
		peers := make([]string, 0, clusterValidatorCount-1)
		for j := range members {
			if i == j {
				continue
			}
			peers = append(peers, fmt.Sprintf("%s@127.0.0.1:%d", members[j].nodeKey.ID(), members[j].p2pPort))
		}
		members[i].cfg.P2P.PersistentPeers = joinPeers(peers)
		if err := members[i].cfg.ValidateBasic(); err != nil {
			t.Fatalf("validate validator %d config: %v", i+1, err)
		}
		config.WriteConfigFile(filepath.Join(members[i].home, "config", "config.toml"), members[i].cfg)

		if err := genesis.SaveAs(members[i].cfg.GenesisFile()); err != nil {
			t.Fatalf("write validator %d genesis: %v", i+1, err)
		}
		rpc, err := httpclient.New(fmt.Sprintf("http://127.0.0.1:%d", members[i].rpcPort), "/websocket")
		if err != nil {
			t.Fatalf("create validator %d RPC client: %v", i+1, err)
		}
		members[i].rpc = rpc
	}

	started := make([]*node.Node, 0, clusterValidatorCount)
	t.Cleanup(func() {
		for i := len(started) - 1; i >= 0; i-- {
			if err := started[i].Stop(); err != nil {
				t.Errorf("stop test validator: %v", err)
			}
			started[i].Wait()
		}
	})

	for i := range members {
		member := &members[i]
		application, err := newApp(filepath.Join(member.home, "app", "state.json"))
		if err != nil {
			t.Fatalf("create validator %d app: %v", i+1, err)
		}
		cometNode, err := node.NewNode(
			member.cfg,
			member.privVal,
			member.nodeKey,
			proxy.NewLocalClientCreator(abci.Application(application)),
			node.DefaultGenesisDocProviderFunc(member.cfg),
			config.DefaultDBProvider,
			node.DefaultMetricsProvider(member.cfg.Instrumentation),
			log.NewNopLogger(),
		)
		if err != nil {
			t.Fatalf("create validator %d CometBFT node: %v", i+1, err)
		}
		if err := cometNode.Start(); err != nil {
			t.Fatalf("start validator %d CometBFT node: %v", i+1, err)
		}
		started = append(started, cometNode)
	}

	waitForClusterCommit(t, members, 3, 45*time.Second)
}

func clusterGenesis(t *testing.T, validators []cmtTypes.GenesisValidator) *cmtTypes.GenesisDoc {
	t.Helper()
	appState, err := json.Marshal(defaultGenesisState())
	if err != nil {
		t.Fatalf("encode isolated genesis app state: %v", err)
	}
	genesis := &cmtTypes.GenesisDoc{
		GenesisTime:     time.Now().UTC(),
		ChainID:         ChainID,
		InitialHeight:   1,
		ConsensusParams: cmtTypes.DefaultConsensusParams(),
		Validators:      append([]cmtTypes.GenesisValidator(nil), validators...),
		AppState:        appState,
	}
	if err := genesis.ValidateAndComplete(); err != nil {
		t.Fatalf("validate isolated multi-validator genesis: %v", err)
	}
	return genesis
}

func waitForClusterCommit(t *testing.T, members []clusterMember, minimumHeight int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	lastStatus := make([]string, len(members))

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		allReady := true
		var expectedHeight int64
		var expectedBlockHash, expectedAppHash string

		for i, member := range members {
			status, err := member.rpc.Status(ctx)
			if err != nil {
				lastStatus[i] = fmt.Sprintf("status error: %v", err)
				allReady = false
				continue
			}
			netInfo, err := member.rpc.NetInfo(ctx)
			if err != nil {
				lastStatus[i] = fmt.Sprintf("network info error: %v", err)
				allReady = false
				continue
			}
			height := status.SyncInfo.LatestBlockHeight
			lastStatus[i] = fmt.Sprintf("height=%d peers=%d", height, netInfo.NPeers)
			if height < minimumHeight || netInfo.NPeers < clusterValidatorCount-1 {
				allReady = false
				continue
			}
			if expectedHeight == 0 {
				expectedHeight = height
				expectedBlockHash = status.SyncInfo.LatestBlockHash.String()
				expectedAppHash = status.SyncInfo.LatestAppHash.String()
				continue
			}
			if height != expectedHeight ||
				status.SyncInfo.LatestBlockHash.String() != expectedBlockHash ||
				status.SyncInfo.LatestAppHash.String() != expectedAppHash {
				allReady = false
			}
		}
		cancel()

		if allReady {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("three-validator cluster did not reach matching height >= %d with two peers each: %v", minimumHeight, lastStatus)
}

func reserveClusterPort(t *testing.T, used map[int]struct{}) int {
	t.Helper()
	for {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve local test port: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatalf("release reserved local test port: %v", err)
		}
		if _, exists := used[port]; exists {
			continue
		}
		used[port] = struct{}{}
		return port
	}
}

func mustPortFromAddress(t *testing.T, address string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(address[len("tcp://"):])
	if err != nil {
		t.Fatalf("parse local RPC address %q: %v", address, err)
	}
	port := 0
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatalf("parse local RPC port %q: %v", portText, err)
	}
	return port
}

func joinPeers(peers []string) string {
	result := ""
	for i, peer := range peers {
		if i > 0 {
			result += ","
		}
		result += peer
	}
	return result
}
