package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSignedEVMTransactionSurvivesBlockAndRestart(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := crypto.PubkeyToAddress(key.PublicKey)
	recipient := common.HexToAddress("0x0000000000000000000000000000000000000100")
	chainID := big.NewInt(31337)
	genesis := &GenesisConfig{
		ChainID:            chainID.Int64(),
		NetworkName:        "EVM integration test",
		EVMActivationBlock: 1,
		GasLimit:           30_000_000,
		Difficulty:         big.NewInt(1),
		Validators:         []string{sender.Hex()},
		Alloc: []GenesisAlloc{{
			Address: sender.Hex(),
			Balance: new(big.Int).Mul(big.NewInt(5), big.NewInt(1_000_000_000_000_000_000)),
		}},
	}
	dataDir := t.TempDir()
	chain := NewChain(genesis, dataDir)
	if available, err := chain.EVMExecutionAvailable(); err != nil || !available {
		t.Fatalf("EVM execution unavailable: available=%v err=%v", available, err)
	}

	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(3_000_000_000),
		Gas:       21_000,
		To:        &recipient,
		Value:     big.NewInt(7),
	})
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		chain.Close()
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		chain.Close()
		t.Fatal(err)
	}
	hash, err := chain.SubmitRawTransaction(raw)
	if err != nil {
		chain.Close()
		t.Fatalf("submit signed transaction: %v", err)
	}
	if hash != signed.Hash().Hex() {
		chain.Close()
		t.Fatalf("submitted hash %s, want %s", hash, signed.Hash().Hex())
	}
	if got := chain.PendingNonce(sender.Hex()); got != 1 {
		chain.Close()
		t.Fatalf("pending nonce %d, want 1", got)
	}

	block := NewBlock(chain.Head().Header, sender.Hex(), chain.PendingTransactions(10))
	if err := chain.InsertBlock(block); err != nil {
		chain.Close()
		t.Fatalf("include signed transaction in block: %v", err)
	}
	confirmed, ok := chain.GetTransaction(hash)
	if !ok {
		chain.Close()
		t.Fatal("confirmed transaction is missing from the index")
	}
	if confirmed.Status != "success" || confirmed.BlockNum != 1 || len(confirmed.Receipt) == 0 {
		chain.Close()
		t.Fatalf("unexpected confirmed transaction: status=%s block=%d receipt=%s", confirmed.Status, confirmed.BlockNum, confirmed.Receipt)
	}
	if got := chain.GetBalance(recipient.Hex()); got.Cmp(big.NewInt(7)) != 0 {
		chain.Close()
		t.Fatalf("recipient balance %s, want 7", got)
	}
	if block.Header.GasUsed != 21_000 || block.Header.StateRoot == "" || block.Header.TxRoot == "" || block.Header.ReceiptRoot == "" {
		chain.Close()
		t.Fatalf("EVM block roots/gas were not recorded: %#v", block.Header)
	}
	chain.Close()

	reopened := NewChain(genesis, dataDir)
	defer reopened.Close()
	if reopened.Height() != 1 {
		t.Fatalf("reopened chain height %d, want 1", reopened.Height())
	}
	if got := reopened.GetBalance(recipient.Hex()); got.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("persisted recipient balance %s, want 7", got)
	}
	if _, ok := reopened.GetTransaction(hash); !ok {
		t.Fatal("confirmed EVM transaction was not restored after restart")
	}
}
