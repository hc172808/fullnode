package main

import (
	"context"
	"encoding/json"
	"math/big"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
)

func TestPrototypeGenesisHasTwoBillionGYDS(t *testing.T) {
	state := defaultGenesisState()
	if err := validateGenesisState(state); err != nil {
		t.Fatal(err)
	}
	want := new(big.Int)
	want.SetString(toBaseUnits(NativeSupplyGYDS), 10)
	if got := totalSupply(state.Balances); got.Cmp(want) != 0 {
		t.Fatalf("total supply = %s; want %s", got, want)
	}
}

func TestInitChainAndSupplyQuery(t *testing.T) {
	app, err := newApp("")
	if err != nil {
		t.Fatal(err)
	}
	stateBytes, err := json.Marshal(defaultGenesisState())
	if err != nil {
		t.Fatal(err)
	}
	init, err := app.InitChain(context.Background(), &abci.RequestInitChain{
		ChainId:       ChainID,
		AppStateBytes: stateBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(init.AppHash) == 0 {
		t.Fatal("InitChain did not return an app hash")
	}

	query, err := app.Query(context.Background(), &abci.RequestQuery{Path: "/supply"})
	if err != nil || query.Code != 0 {
		t.Fatalf("supply query failed: response=%+v err=%v", query, err)
	}
	var supply map[string]string
	if err := json.Unmarshal(query.Value, &supply); err != nil {
		t.Fatal(err)
	}
	if supply["totalSupply"] != "2000000000" || supply["symbol"] != "GYDS" {
		t.Fatalf("unexpected supply response: %s", query.Value)
	}
}

func TestPrototypeRejectsTransactions(t *testing.T) {
	app, err := newApp("")
	if err != nil {
		t.Fatal(err)
	}
	check, err := app.CheckTx(context.Background(), &abci.RequestCheckTx{Tx: []byte("anything")})
	if err != nil || check.Code == 0 {
		t.Fatalf("CheckTx accepted a transaction: response=%+v err=%v", check, err)
	}
	proposal, err := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: [][]byte{[]byte("anything")}})
	if err != nil || proposal.Status != abci.ResponseProcessProposal_REJECT {
		t.Fatalf("ProcessProposal did not reject a transaction: response=%+v err=%v", proposal, err)
	}
}

func TestCommitPersistsHeightAndStateHash(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "app", "state.json")
	app, err := newApp(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	stateBytes, _ := json.Marshal(defaultGenesisState())
	if _, err := app.InitChain(context.Background(), &abci.RequestInitChain{ChainId: ChainID, AppStateBytes: stateBytes}); err != nil {
		t.Fatal(err)
	}
	finalized, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.AppHash) == 0 {
		t.Fatal("FinalizeBlock did not return an app hash")
	}
	if _, err := app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
		t.Fatal(err)
	}

	restarted, err := newApp(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := restarted.Info(context.Background(), &abci.RequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if info.LastBlockHeight != 1 {
		t.Fatalf("restored height = %d; want 1", info.LastBlockHeight)
	}
	if string(info.LastBlockAppHash) != string(finalized.AppHash) {
		t.Fatal("restored app hash differs from committed state")
	}
}
