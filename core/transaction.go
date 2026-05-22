package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"
)

type TxType uint8

const (
	TxTypeTransfer   TxType = 0
	TxTypeContract   TxType = 1
	TxTypeStake      TxType = 2
	TxTypeUnstake    TxType = 3
	TxTypeBridge     TxType = 4
)

type Transaction struct {
	Hash      string   `json:"hash"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Value     *big.Int `json:"value"`
	GasLimit  uint64   `json:"gasLimit"`
	GasPrice  *big.Int `json:"gasPrice"`
	GasUsed   uint64   `json:"gasUsed"`
	Nonce     uint64   `json:"nonce"`
	Data      []byte   `json:"data,omitempty"`
	Type      TxType   `json:"type"`
	Status    string   `json:"status"`
	Timestamp int64    `json:"timestamp"`
	BlockNum  uint64   `json:"blockNumber"`
}

func NewTransaction(from, to string, value *big.Int, nonce uint64, data []byte) *Transaction {
	tx := &Transaction{
		From:      from,
		To:        to,
		Value:     value,
		GasLimit:  21_000,
		GasPrice:  big.NewInt(1_000_000_000),
		GasUsed:   21_000,
		Nonce:     nonce,
		Data:      data,
		Type:      TxTypeTransfer,
		Status:    "pending",
		Timestamp: time.Now().Unix(),
	}
	if len(data) > 0 {
		tx.Type = TxTypeContract
		tx.GasLimit = 200_000
		tx.GasUsed = 80_000 + uint64(len(data)*68)
	}
	tx.Hash = tx.computeHash()
	return tx
}

func (tx *Transaction) computeHash() string {
	raw := fmt.Sprintf("%s:%s:%s:%d:%d:%d",
		tx.From, tx.To,
		tx.Value.String(),
		tx.Nonce, tx.GasPrice.Int64(), tx.Timestamp)
	sum := sha256.Sum256([]byte(raw))
	return "0x" + hex.EncodeToString(sum[:])
}

func (tx *Transaction) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"hash":        tx.Hash,
		"from":        tx.From,
		"to":          tx.To,
		"value":       tx.Value.String(),
		"gasLimit":    tx.GasLimit,
		"gasPrice":    tx.GasPrice.String(),
		"gasUsed":     tx.GasUsed,
		"nonce":       tx.Nonce,
		"type":        tx.Type,
		"status":      tx.Status,
		"timestamp":   tx.Timestamp,
		"blockNumber": tx.BlockNum,
	}
}
