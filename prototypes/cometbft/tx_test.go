package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestCheckTxAndProcessProposalValidateSignedTransfers(t *testing.T) {
	app, key, recipient := newFundedNativeApp(t, "")
	valid := signedNativeTransferForTest(t, key, 0, recipient, big.NewInt(7), ChainID)
	check, err := app.CheckTx(context.Background(), &abci.RequestCheckTx{Tx: valid})
	if err != nil || check.Code != 0 {
		t.Fatalf("valid signed transfer rejected: response=%+v err=%v", check, err)
	}
	proposal, err := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: [][]byte{valid}})
	if err != nil || proposal.Status != abci.ResponseProcessProposal_ACCEPT {
		t.Fatalf("valid signed transfer proposal rejected: response=%+v err=%v", proposal, err)
	}

	wrongTestnetChain := signedNativeTransferForTest(t, key, 0, recipient, big.NewInt(7), "198281")
	wrongMainnetChain := signedNativeTransferForTest(t, key, 0, recipient, big.NewInt(7), "198282")
	unprotected := unprotectedNativeTransferForTest(t, key, 0, recipient, big.NewInt(7))
	tampered := append([]byte(nil), valid...)
	tampered[len(tampered)-1] ^= 1
	totalSupply, ok := new(big.Int).SetString(toBaseUnits(NativeSupplyGYDS), 10)
	if !ok {
		t.Fatal("invalid prototype genesis supply")
	}
	overspend := new(big.Int).Add(totalSupply, big.NewInt(1))
	invalidTransactions := map[string][]byte{
		"malformed":          []byte("not an Ethereum transaction"),
		"testnet chain":      wrongTestnetChain,
		"mainnet chain":      wrongMainnetChain,
		"unprotected":        unprotected,
		"bad signature":      tampered,
		"wrong nonce":        signedNativeTransferForTest(t, key, 1, recipient, big.NewInt(7), ChainID),
		"insufficient funds": signedNativeTransferForTest(t, key, 0, recipient, overspend, ChainID),
	}
	for name, raw := range invalidTransactions {
		t.Run(name, func(t *testing.T) {
			check, err := app.CheckTx(context.Background(), &abci.RequestCheckTx{Tx: raw})
			if err != nil || check.Code == 0 {
				t.Fatalf("CheckTx accepted invalid transaction: response=%+v err=%v", check, err)
			}
			proposal, err := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: [][]byte{raw}})
			if err != nil || proposal.Status != abci.ResponseProcessProposal_REJECT {
				t.Fatalf("ProcessProposal accepted invalid transaction: response=%+v err=%v", proposal, err)
			}
		})
	}
}

func TestPrepareProposalSelectsValidOrderedTransfers(t *testing.T) {
	app, key, recipient := newFundedNativeApp(t, "")
	first := signedNativeTransferForTest(t, key, 0, recipient, big.NewInt(5), ChainID)
	wrongChain := signedNativeTransferForTest(t, key, 1, recipient, big.NewInt(3), "198281")
	second := signedNativeTransferForTest(t, key, 1, recipient, big.NewInt(7), ChainID)

	prepared, err := app.PrepareProposal(context.Background(), &abci.RequestPrepareProposal{
		Txs:        [][]byte{first, wrongChain, second},
		MaxTxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Txs) != 2 || !bytes.Equal(prepared.Txs[0], first) || !bytes.Equal(prepared.Txs[1], second) {
		t.Fatalf("prepared transactions are not the valid ordered sequence: %x", prepared.Txs)
	}
	processed, err := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: prepared.Txs})
	if err != nil || processed.Status != abci.ResponseProcessProposal_ACCEPT {
		t.Fatalf("prepared proposal did not validate: response=%+v err=%v", processed, err)
	}
}

func TestPrototypeChainIDDoesNotCollideWithConfiguredNetworks(t *testing.T) {
	for _, configuredChainID := range []string{"198281", "198282", "198284", "31337"} {
		if ChainID == configuredChainID {
			t.Fatalf("prototype chain ID %s collides with configured network ID", ChainID)
		}
	}
}

func TestFinalizeBlockPersistsSignedTransferBalancesAndNonce(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "app", "state.json")
	app, key, recipient := newFundedNativeApp(t, stateFile)
	amount := new(big.Int).SetUint64(1_234_567_890)
	raw := signedNativeTransferForTest(t, key, 0, recipient, amount, ChainID)

	check, err := app.CheckTx(context.Background(), &abci.RequestCheckTx{Tx: raw})
	if err != nil || check.Code != 0 {
		t.Fatalf("CheckTx rejected signed transfer: response=%+v err=%v", check, err)
	}
	processed, err := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: [][]byte{raw}})
	if err != nil || processed.Status != abci.ResponseProcessProposal_ACCEPT {
		t.Fatalf("ProcessProposal rejected signed transfer: response=%+v err=%v", processed, err)
	}

	finalized, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Txs: [][]byte{raw}})
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.TxResults) != 1 || finalized.TxResults[0].Code != 0 {
		t.Fatalf("transfer execution failed: %+v", finalized.TxResults)
	}
	if _, err := app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
		t.Fatal(err)
	}

	sender := addressKey(ethcrypto.PubkeyToAddress(key.PublicKey))
	// The sender starts with the full fixed supply; subtract the transfer amount.
	initialBalance, ok := new(big.Int).SetString(toBaseUnits(NativeSupplyGYDS), 10)
	if !ok {
		t.Fatal("invalid prototype supply")
	}
	expectedSender := new(big.Int).Sub(initialBalance, amount)
	assertQueryValue(t, app, "/balance/"+sender, expectedSender.String())
	assertQueryValue(t, app, "/balance/"+addressKey(recipient), amount.String())
	assertQueryValue(t, app, "/nonce/"+sender, "1")

	supply, err := app.Query(context.Background(), &abci.RequestQuery{Path: "/supply"})
	if err != nil || supply.Code != 0 {
		t.Fatalf("supply query failed: response=%+v err=%v", supply, err)
	}
	var supplyJSON map[string]string
	if err := json.Unmarshal(supply.Value, &supplyJSON); err != nil {
		t.Fatal(err)
	}
	if supplyJSON["totalSupplyBaseUnits"] != toBaseUnits(NativeSupplyGYDS) {
		t.Fatalf("transfer changed supply: %s", supply.Value)
	}

	restarted, err := newApp(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := restarted.Info(context.Background(), &abci.RequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if info.LastBlockHeight != 1 || !bytes.Equal(info.LastBlockAppHash, finalized.AppHash) {
		t.Fatalf("restarted state differs from committed state: height=%d hash=%X", info.LastBlockHeight, info.LastBlockAppHash)
	}
	assertQueryValue(t, restarted, "/balance/"+addressKey(recipient), amount.String())
	assertQueryValue(t, restarted, "/nonce/"+sender, "1")
}

func TestFinalizeBlockInvalidTransferDoesNotChangeLedger(t *testing.T) {
	app, key, recipient := newFundedNativeApp(t, "")
	raw := signedNativeTransferForTest(t, key, 1, recipient, big.NewInt(11), ChainID)

	finalized, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Txs: [][]byte{raw}})
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.TxResults) != 1 || finalized.TxResults[0].Code == 0 {
		t.Fatalf("invalid nonce transaction unexpectedly succeeded: %+v", finalized.TxResults)
	}
	if _, err := app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
		t.Fatal(err)
	}

	sender := addressKey(ethcrypto.PubkeyToAddress(key.PublicKey))
	assertQueryValue(t, app, "/balance/"+sender, toBaseUnits(NativeSupplyGYDS))
	assertQueryValue(t, app, "/balance/"+addressKey(recipient), "0")
	assertQueryValue(t, app, "/nonce/"+sender, "0")
}

func newFundedNativeApp(t *testing.T, stateFile string) (*app, *ecdsa.PrivateKey, common.Address) {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipientKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderAddress := ethcrypto.PubkeyToAddress(key.PublicKey)
	recipient := ethcrypto.PubkeyToAddress(recipientKey.PublicKey)
	for recipient == senderAddress {
		recipientKey, err = ethcrypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		recipient = ethcrypto.PubkeyToAddress(recipientKey.PublicKey)
	}

	app, err := newApp(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	genesisBytes, err := json.Marshal(GenesisState{
		ChainID: ChainID,
		Balances: map[string]string{
			addressKey(senderAddress): toBaseUnits(NativeSupplyGYDS),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.InitChain(context.Background(), &abci.RequestInitChain{
		ChainId:       ChainID,
		AppStateBytes: genesisBytes,
	}); err != nil {
		t.Fatal(err)
	}
	return app, key, recipient
}

func signedNativeTransferForTest(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, to common.Address, amount *big.Int, chainIDText string) []byte {
	t.Helper()
	chainID, ok := new(big.Int).SetString(chainIDText, 10)
	if !ok {
		t.Fatalf("invalid test chain ID %q", chainIDText)
	}
	unsigned := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    new(big.Int).Set(amount),
		Gas:      nativeTransferGasLimit,
		GasPrice: new(big.Int),
	})
	signed, err := types.SignTx(unsigned, types.NewEIP155Signer(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func unprotectedNativeTransferForTest(t *testing.T, key *ecdsa.PrivateKey, nonce uint64, to common.Address, amount *big.Int) []byte {
	t.Helper()
	unsigned := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    new(big.Int).Set(amount),
		Gas:      nativeTransferGasLimit,
		GasPrice: new(big.Int),
	})
	signed, err := types.SignTx(unsigned, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertQueryValue(t *testing.T, app *app, path, want string) {
	t.Helper()
	response, err := app.Query(context.Background(), &abci.RequestQuery{Path: path})
	if err != nil || response.Code != 0 {
		t.Fatalf("query %s failed: response=%+v err=%v", path, response, err)
	}
	if got := string(response.Value); got != want {
		t.Fatalf("query %s = %q; want %q", path, got, want)
	}
}
