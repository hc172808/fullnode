package evm

import (
	"bytes"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestDecodeSignedTransactionEnforcesSignatureAndChainID(t *testing.T) {
	chainID := big.NewInt(31337)
	key, sender := newTestAccount(t)
	to := common.HexToAddress("0x0000000000000000000000000000000000000002")
	tx := signDynamicTx(t, key, chainID, 0, &to, big.NewInt(1), nil, 21_000)
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	decoded, recovered, err := DecodeSignedTransaction(raw, chainID)
	if err != nil {
		t.Fatalf("decode valid signed transaction: %v", err)
	}
	if recovered != sender {
		t.Fatalf("recovered sender %s, want %s", recovered.Hex(), sender.Hex())
	}
	if decoded.Hash() != tx.Hash() {
		t.Fatalf("decoded hash %s, want %s", decoded.Hash(), tx.Hash())
	}

	if _, _, err := DecodeSignedTransaction(raw, big.NewInt(1)); err != ErrWrongChainID {
		t.Fatalf("wrong chain ID error = %v, want %v", err, ErrWrongChainID)
	}
	if _, _, err := DecodeSignedTransaction([]byte{0x02, 0x01}, chainID); err == nil {
		t.Fatal("malformed transaction was accepted")
	}
}

func TestApplyBlockExecutesSignedValueTransfer(t *testing.T) {
	chainID := big.NewInt(31337)
	key, sender := newTestAccount(t)
	recipient := common.HexToAddress("0x0000000000000000000000000000000000000100")
	startingBalance := new(big.Int).Mul(big.NewInt(5), big.NewInt(1_000_000_000_000_000_000))
	engine, err := NewMemory(chainID, map[common.Address]GenesisAccount{
		sender: {Balance: startingBalance},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	tx := signDynamicTx(t, key, chainID, 0, &recipient, big.NewInt(1), nil, 21_000)
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, recovered, err := DecodeSignedTransaction(raw, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != sender {
		t.Fatalf("decoded sender %s, want %s", recovered.Hex(), sender.Hex())
	}

	result, err := engine.ApplyBlock(testBlockContext(1), []*types.Transaction{decoded})
	if err != nil {
		t.Fatalf("apply signed transfer: %v", err)
	}
	if result.StateRoot == (common.Hash{}) || result.StateRoot == types.EmptyRootHash {
		t.Fatalf("unexpected post-transaction state root %s", result.StateRoot.Hex())
	}
	if result.GasUsed != 21_000 {
		t.Fatalf("gas used %d, want 21000", result.GasUsed)
	}
	if len(result.Receipts) != 1 || result.Receipts[0].Status != types.ReceiptStatusSuccessful {
		if len(result.Receipts) == 0 {
			t.Fatalf("no receipt returned")
		}
		t.Fatalf("unexpected receipt count/status/gas: %d/%d/%d", len(result.Receipts), result.Receipts[0].Status, result.Receipts[0].GasUsed)
	}
	if got := engine.Balance(recipient); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("recipient balance %s, want 1", got)
	}
	if got := engine.Nonce(sender); got != 1 {
		t.Fatalf("sender nonce %d, want 1", got)
	}
	expectedSenderBalance := new(big.Int).Sub(startingBalance, big.NewInt(1))
	fee := new(big.Int).Mul(new(big.Int).SetUint64(result.GasUsed), result.Receipts[0].EffectiveGasPrice)
	expectedSenderBalance.Sub(expectedSenderBalance, fee)
	if got := engine.Balance(sender); got.Cmp(expectedSenderBalance) != 0 {
		t.Fatalf("sender balance %s, want %s", got, expectedSenderBalance)
	}
}

func TestApplyBlockDeploysContractAndPersistsState(t *testing.T) {
	chainID := big.NewInt(31337)
	key, sender := newTestAccount(t)
	path := t.TempDir()
	engine, err := Open(path, chainID, map[common.Address]GenesisAccount{
		sender: {Balance: new(big.Int).Mul(big.NewInt(5), big.NewInt(1_000_000_000_000_000_000))},
	})
	if err != nil {
		t.Fatal(err)
	}

	runtimeCode := []byte{
		0x60, 0x2a, // PUSH1 42
		0x60, 0x00, // PUSH1 0
		0x55,       // SSTORE
		0x60, 0x00, // PUSH1 0
		0x54,       // SLOAD
		0x60, 0x00, // PUSH1 0
		0x52,       // MSTORE
		0x60, 0x20, // PUSH1 32
		0x60, 0x00, // PUSH1 0
		0xf3, // RETURN
	}
	initCode := append([]byte{
		0x60, byte(len(runtimeCode)),
		0x60, 0x0c,
		0x60, 0x00,
		0x39,
		0x60, byte(len(runtimeCode)),
		0x60, 0x00,
		0xf3,
	}, runtimeCode...)
	createTx := signDynamicTx(t, key, chainID, 0, nil, new(big.Int), initCode, 200_000)
	contract := crypto.CreateAddress(sender, 0)
	callTx := signDynamicTx(t, key, chainID, 1, &contract, new(big.Int), nil, 100_000)

	result, err := engine.ApplyBlock(testBlockContext(1), []*types.Transaction{createTx, callTx})
	if err != nil {
		_ = engine.Close()
		t.Fatalf("deploy and call contract: %v", err)
	}
	if len(result.Receipts) != 2 {
		_ = engine.Close()
		t.Fatalf("got %d receipts, want 2", len(result.Receipts))
	}
	if result.Receipts[0].ContractAddress != contract {
		_ = engine.Close()
		t.Fatalf("contract address %s, want %s", result.Receipts[0].ContractAddress.Hex(), contract.Hex())
	}
	if result.Receipts[0].Status != types.ReceiptStatusSuccessful || result.Receipts[1].Status != types.ReceiptStatusSuccessful {
		_ = engine.Close()
		t.Fatalf("contract execution failed: statuses %d, %d", result.Receipts[0].Status, result.Receipts[1].Status)
	}
	if got := engine.Code(contract); !bytes.Equal(got, runtimeCode) {
		_ = engine.Close()
		t.Fatalf("deployed code %x, want %x", got, runtimeCode)
	}
	if got := engine.StorageAt(contract, common.Hash{}); got != common.BigToHash(big.NewInt(42)) {
		_ = engine.Close()
		t.Fatalf("storage slot 0 = %s, want 42", got.Hex())
	}
	root := engine.StateRoot()
	if err := engine.Close(); err != nil {
		t.Fatalf("close state database: %v", err)
	}

	reopened, err := Open(path, chainID, nil)
	if err != nil {
		t.Fatalf("reopen EVM state: %v", err)
	}
	defer reopened.Close()
	if reopened.Height() != 1 {
		t.Fatalf("reopened height %d, want 1", reopened.Height())
	}
	if reopened.StateRoot() != root {
		t.Fatalf("reopened root %s, want %s", reopened.StateRoot().Hex(), root.Hex())
	}
	if got := reopened.Code(contract); !bytes.Equal(got, runtimeCode) {
		t.Fatalf("reopened contract code %x, want %x", got, runtimeCode)
	}
	if got := reopened.StorageAt(contract, common.Hash{}); got != common.BigToHash(big.NewInt(42)) {
		t.Fatalf("reopened storage slot 0 = %s, want 42", got.Hex())
	}
}

func TestInvalidBlockDoesNotAdvanceEVMState(t *testing.T) {
	chainID := big.NewInt(31337)
	key, sender := newTestAccount(t)
	engine, err := NewMemory(chainID, map[common.Address]GenesisAccount{
		sender: {Balance: new(big.Int).Mul(big.NewInt(1), big.NewInt(1_000_000_000_000_000_000))},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	beforeRoot := engine.StateRoot()
	beforeBalance := engine.Balance(sender)
	to := common.HexToAddress("0x0000000000000000000000000000000000000003")
	tx := signDynamicTx(t, key, chainID, 1, &to, big.NewInt(1), nil, 21_000)
	if _, err := engine.ApplyBlock(testBlockContext(1), []*types.Transaction{tx}); err == nil {
		t.Fatal("transaction with an invalid nonce was accepted")
	}
	if engine.Height() != 0 {
		t.Fatalf("height advanced to %d after rejected block", engine.Height())
	}
	if engine.StateRoot() != beforeRoot {
		t.Fatal("state root changed after rejected block")
	}
	if got := engine.Balance(sender); got.Cmp(beforeBalance) != 0 {
		t.Fatalf("sender balance changed to %s after rejected block", got)
	}
}

func newTestAccount(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key, crypto.PubkeyToAddress(key.PublicKey)
}

func signDynamicTx(t *testing.T, key *ecdsa.PrivateKey, chainID *big.Int, nonce uint64, to *common.Address, value *big.Int, data []byte, gas uint64) *types.Transaction {
	t.Helper()
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID:   new(big.Int).Set(chainID),
		Nonce:     nonce,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(3_000_000_000),
		Gas:       gas,
		To:        to,
		Value:     new(big.Int).Set(value),
		Data:      bytes.Clone(data),
	})
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func testBlockContext(number uint64) BlockContext {
	return BlockContext{
		Number:    number,
		Timestamp: 1_790_000_000,
		GasLimit:  30_000_000,
		BaseFee:   big.NewInt(1_000_000_000),
		Random:    func() *common.Hash { hash := common.Hash{}; return &hash }(),
	}
}
