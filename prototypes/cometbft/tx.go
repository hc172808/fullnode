package main

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	nativeTransferGasLimit = uint64(21_000)
	maxPrototypeTxBytes    = 64 * 1024
)

type nativeTransfer struct {
	from  common.Address
	to    common.Address
	nonce uint64
	value *big.Int
}

func decodeNativeTransfer(raw []byte) (nativeTransfer, error) {
	if len(raw) == 0 || len(raw) > maxPrototypeTxBytes {
		return nativeTransfer{}, fmt.Errorf("transaction size must be between 1 and %d bytes", maxPrototypeTxBytes)
	}

	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nativeTransfer{}, fmt.Errorf("decode Ethereum transaction: %w", err)
	}
	if tx.Type() != types.LegacyTxType {
		return nativeTransfer{}, fmt.Errorf("prototype accepts only legacy EIP-155 transactions")
	}
	canonical, err := tx.MarshalBinary()
	if err != nil {
		return nativeTransfer{}, fmt.Errorf("encode canonical transaction: %w", err)
	}
	if !bytes.Equal(canonical, raw) {
		return nativeTransfer{}, fmt.Errorf("transaction encoding is not canonical")
	}

	chainID, ok := new(big.Int).SetString(ChainID, 10)
	if !ok {
		return nativeTransfer{}, fmt.Errorf("invalid prototype chain ID configuration")
	}
	if !tx.Protected() || tx.ChainId().Cmp(chainID) != 0 {
		return nativeTransfer{}, fmt.Errorf("transaction must be EIP-155 protected for chain ID %s", ChainID)
	}
	if tx.To() == nil || len(tx.Data()) != 0 {
		return nativeTransfer{}, fmt.Errorf("prototype accepts only native value transfers")
	}
	if tx.Gas() != nativeTransferGasLimit {
		return nativeTransfer{}, fmt.Errorf("native transfer gas limit must be %d", nativeTransferGasLimit)
	}
	if tx.GasPrice() == nil || tx.GasPrice().Sign() != 0 {
		return nativeTransfer{}, fmt.Errorf("prototype native transfers do not charge a gas fee")
	}
	if tx.Value() == nil || tx.Value().Sign() <= 0 || tx.Value().BitLen() > 256 {
		return nativeTransfer{}, fmt.Errorf("transfer value must be a positive uint256")
	}

	signer := types.NewEIP155Signer(chainID)
	from, err := types.Sender(signer, &tx)
	if err != nil {
		return nativeTransfer{}, fmt.Errorf("recover transaction signer: %w", err)
	}
	return nativeTransfer{
		from:  from,
		to:    *tx.To(),
		nonce: tx.Nonce(),
		value: new(big.Int).Set(tx.Value()),
	}, nil
}

func applyNativeTransfer(state *appState, raw []byte) error {
	if state == nil || state.ChainID != ChainID || state.Balances == nil {
		return fmt.Errorf("application state is not initialized for chain %s", ChainID)
	}
	tx, err := decodeNativeTransfer(raw)
	if err != nil {
		return err
	}

	from := addressKey(tx.from)
	to := addressKey(tx.to)
	expectedNonce := state.Nonces[from]
	if tx.nonce != expectedNonce {
		return fmt.Errorf("invalid nonce for %s: got %d, want %d", from, tx.nonce, expectedNonce)
	}
	if expectedNonce == math.MaxUint64 {
		return fmt.Errorf("nonce exhausted for %s", from)
	}

	fromBalance, ok := new(big.Int).SetString(state.Balances[from], 10)
	if !ok || fromBalance.Sign() < 0 || fromBalance.BitLen() > 256 {
		return fmt.Errorf("sender %s has invalid balance state", from)
	}
	if fromBalance.Cmp(tx.value) < 0 {
		return fmt.Errorf("insufficient native balance for %s", from)
	}

	if from == to {
		if state.Nonces == nil {
			state.Nonces = make(map[string]uint64)
		}
		state.Nonces[from] = expectedNonce + 1
		return nil
	}

	toBalance := new(big.Int)
	if balanceText, exists := state.Balances[to]; exists {
		if _, ok := toBalance.SetString(balanceText, 10); !ok || toBalance.Sign() < 0 || toBalance.BitLen() > 256 {
			return fmt.Errorf("recipient %s has invalid balance state", to)
		}
	}
	newToBalance := new(big.Int).Add(toBalance, tx.value)
	if newToBalance.BitLen() > 256 {
		return fmt.Errorf("recipient balance exceeds uint256")
	}
	newFromBalance := new(big.Int).Sub(fromBalance, tx.value)

	state.Balances[from] = newFromBalance.String()
	state.Balances[to] = newToBalance.String()
	if state.Nonces == nil {
		state.Nonces = make(map[string]uint64)
	}
	state.Nonces[from] = expectedNonce + 1
	return nil
}

func addressKey(address common.Address) string {
	return strings.ToLower(address.Hex())
}
