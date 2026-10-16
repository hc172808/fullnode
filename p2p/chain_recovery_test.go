package p2p

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/gydschain/fullnode/core"
)

// Exercise real serialized blocks and persistent native state, not placeholder
// block payloads. This is a local integration test, not a consensus-safety test.
func TestAuthenticatedChainReplayAndRestartRecovery(t *testing.T) {
	const sender = "0x00000000000000000000000000000000000000a1"
	const recipient = "0x00000000000000000000000000000000000000b2"
	genesis := &core.GenesisConfig{
		ChainID: 31337, NetworkName: "Local recovery fixture",
		GasLimit: 30_000_000, Difficulty: big.NewInt(1),
		Validators: []string{sender},
		Alloc: []core.GenesisAlloc{{
			Address: sender, Balance: big.NewInt(1_000_000_000_000_000_000),
		}},
	}
	sourceDir, followerDir := t.TempDir(), t.TempDir()
	source := core.NewChain(genesis, sourceDir)
	defer source.Close()
	sourceKey, err := LoadOrCreateNodeKey(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	followerKey, err := LoadOrCreateNodeKey(followerDir)
	if err != nil {
		t.Fatal(err)
	}
	followerID := followerKey.ID()
	var transactions []*core.Transaction
	produce := func(count int) {
		t.Helper()
		for range count {
			tx := core.NewTransaction(sender, recipient, big.NewInt(42), source.GetNonce(sender), nil)
			block := core.NewBlock(source.Head().Header, sender, []*core.Transaction{tx})
			if err := source.InsertBlock(block); err != nil {
				t.Fatalf("produce source block: %v", err)
			}
			transactions = append(transactions, tx)
		}
	}
	checkState := func(follower *core.Chain) {
		t.Helper()
		if follower.Height() != source.Height() || follower.Head().Hash != source.Head().Hash {
			t.Fatalf("heads differ: source=%d/%s follower=%d/%s",
				source.Height(), source.Head().Hash, follower.Height(), follower.Head().Hash)
		}
		for _, address := range []string{sender, recipient} {
			if follower.GetBalance(address).Cmp(source.GetBalance(address)) != 0 ||
				follower.GetNonce(address) != source.GetNonce(address) {
				t.Fatalf("replayed account differs for %s", address)
			}
		}
		for _, tx := range transactions {
			if restored, ok := follower.GetTransaction(tx.Hash); !ok || restored.Hash != tx.Hash {
				t.Fatalf("missing replayed transaction %s", tx.Hash)
			}
		}
		for height := uint64(0); height <= source.Height(); height++ {
			want, err := source.GetByNumber(height)
			if err != nil {
				t.Fatal(err)
			}
			got, err := follower.GetByNumber(height)
			if err != nil || got.Hash != want.Hash || got.Header.ParentHash != want.Header.ParentHash {
				t.Fatalf("block history differs at height %d: %v", height, err)
			}
		}
	}
	produce(3)
	for round := range 2 {
		if round == 1 {
			// The follower was offline while the source advanced.
			produce(2)
		}
		follower := core.NewChain(genesis, followerDir)
		// Cleanup the round's TCP connections before closing its chain.
		ok := t.Run([]string{"initial catch-up", "catch-up after reopen"}[round], func(t *testing.T) {
			key, err := LoadOrCreateNodeKey(followerDir)
			if err != nil {
				t.Fatal(err)
			}
			if key.ID() != followerID {
				t.Fatal("follower identity changed after restart")
			}
			if got, want := follower.Height(), uint64(round*3); got != want {
				t.Fatalf("restored height = %d, want %d", got, want)
			}
			upstream := NewServer(0, genesis.ChainID, source.Height)
			downstream := NewServer(0, genesis.ChainID, follower.Height)
			hash := core.GenesisBlock(genesis).Hash
			upstream.SetGenesisHash(hash)
			downstream.SetGenesisHash(hash)
			upstream.SetAuth(sourceKey, true, []string{key.ID()})
			downstream.SetAuth(key, true, []string{sourceKey.ID()})
			addr := listenPeerTestServer(t, upstream)
			listenPeerTestServer(t, downstream)
			upstream.SetBlockProvider(func(from uint64, count int) json.RawMessage {
				blocks := make([]*core.Block, 0, count)
				for height := from; height <= source.Height() && len(blocks) < count; height++ {
					block, err := source.GetByNumber(height)
					if err != nil {
						return nil
					}
					blocks = append(blocks, block)
				}
				payload, _ := json.Marshal(blocks)
				return payload
			})
			batches := make(chan json.RawMessage, 1)
			downstream.OnMessage(func(_ *Peer, msg Message) {
				if msg.Type == MsgBlocks {
					select {
					case batches <- msg.Payload:
					default:
					}
				}
			})
			if err := downstream.ConnectTo(addr); err != nil {
				t.Fatal(err)
			}
			waitForPeerState(t, "mutually authorized peers and advertised height", func() bool {
				return upstream.PeerCount() == 1 && downstream.PeerCount() == 1 &&
					downstream.MaxPeerHeight() == source.Height()
			})
			downstream.RequestBlocks(follower.Height()+1, 50)
			select {
			case payload := <-batches:
				var blocks []*core.Block
				if err := json.Unmarshal(payload, &blocks); err != nil {
					t.Fatal(err)
				}
				if len(blocks) != 3-round {
					t.Fatalf("received %d blocks, want %d", len(blocks), 3-round)
				}
				for _, block := range blocks {
					if err := follower.InsertBlock(block); err != nil {
						t.Fatalf("replay received block: %v", err)
					}
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out receiving real chain blocks")
			}
			checkState(follower)
		})
		follower.Close()
		if !ok {
			return
		}
	}
	reopened := core.NewChain(genesis, followerDir)
	defer reopened.Close()
	checkState(reopened)
}
