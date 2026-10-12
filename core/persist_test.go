package core

import (
	"math/big"
	"testing"
)

func TestNativeBlockStateAndTransactionIndexSurviveRestart(t *testing.T) {
	const sender = "0x00000000000000000000000000000000000000a1"
	const recipient = "0x00000000000000000000000000000000000000b2"
	genesis := &GenesisConfig{
		ChainID:     31337,
		NetworkName: "Persistence test",
		GasLimit:    30_000_000,
		Difficulty:  big.NewInt(1),
		Validators:  []string{sender},
		Alloc: []GenesisAlloc{{
			Address: sender,
			Balance: big.NewInt(1_000_000_000_000_000_000),
		}},
	}
	dataDir := t.TempDir()
	chain := NewChain(genesis, dataDir)

	tx := NewTransaction(sender, recipient, big.NewInt(42), 0, nil)
	block := NewBlock(chain.Head().Header, sender, []*Transaction{tx})
	if err := chain.InsertBlock(block); err != nil {
		chain.Close()
		t.Fatalf("insert block: %v", err)
	}
	senderBalance := chain.GetBalance(sender).String()
	chain.Close()

	reopened := NewChain(genesis, dataDir)
	defer reopened.Close()
	if got := reopened.Height(); got != 1 {
		t.Fatalf("reopened height = %d, want 1", got)
	}
	restored, err := reopened.GetByNumber(1)
	if err != nil {
		t.Fatalf("get restored block: %v", err)
	}
	if restored.Hash != block.Hash || restored.Header.ParentHash != block.Header.ParentHash {
		t.Fatalf("restored block identity differs: got hash=%s parent=%s, want hash=%s parent=%s",
			restored.Hash, restored.Header.ParentHash, block.Hash, block.Header.ParentHash)
	}
	if got := reopened.GetBalance(sender).String(); got != senderBalance {
		t.Fatalf("restored sender balance = %s, want %s", got, senderBalance)
	}
	if got := reopened.GetBalance(recipient); got.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("restored recipient balance = %s, want 42", got)
	}
	if got := reopened.GetNonce(sender); got != 1 {
		t.Fatalf("restored sender nonce = %d, want 1", got)
	}
	restoredTx, ok := reopened.GetTransaction(tx.Hash)
	if !ok {
		t.Fatal("confirmed transaction index was not restored")
	}
	if restoredTx.Hash != tx.Hash {
		t.Fatalf("restored transaction hash = %s, want %s", restoredTx.Hash, tx.Hash)
	}
}
