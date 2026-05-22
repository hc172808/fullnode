package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

type Header struct {
	Number     uint64   `json:"number"`
	Hash       string   `json:"hash"`
	ParentHash string   `json:"parentHash"`
	StateRoot  string   `json:"stateRoot"`
	TxRoot     string   `json:"txRoot"`
	ReceiptRoot string  `json:"receiptRoot"`
	Validator  string   `json:"validator"`
	Timestamp  int64    `json:"timestamp"`
	GasLimit   uint64   `json:"gasLimit"`
	GasUsed    uint64   `json:"gasUsed"`
	Difficulty *big.Int `json:"difficulty"`
	ExtraData  []byte   `json:"extraData,omitempty"`
	Size       uint64   `json:"size"`
}

type Block struct {
	Header       *Header        `json:"header"`
	Transactions []*Transaction `json:"transactions"`
	Hash         string         `json:"hash"`
}

func (h *Header) ComputeHash() string {
	data := fmt.Sprintf("%d:%s:%s:%s:%d:%s:%d:%d",
		h.Number, h.ParentHash, h.StateRoot, h.TxRoot,
		h.Timestamp, h.Validator, h.GasUsed, h.GasLimit)
	sum := sha256.Sum256([]byte(data))
	return "0x" + hex.EncodeToString(sum[:])
}

func NewBlock(parent *Header, validator string, txs []*Transaction) *Block {
	var parentHash string
	var number uint64
	if parent != nil {
		parentHash = parent.Hash
		number = parent.Number + 1
	}

	txRoot := computeTxRoot(txs)

	h := &Header{
		Number:      number,
		ParentHash:  parentHash,
		StateRoot:   "0x" + hex.EncodeToString(sha256.New().Sum(nil)),
		TxRoot:      txRoot,
		ReceiptRoot: "0x" + hex.EncodeToString(sha256.New().Sum(nil)),
		Validator:   validator,
		Timestamp:   time.Now().Unix(),
		GasLimit:    30_000_000,
		GasUsed:     estimateGasUsed(txs),
		Difficulty:  big.NewInt(1),
		Size:        estimateBlockSize(txs),
	}
	h.Hash = h.ComputeHash()

	return &Block{
		Header:       h,
		Transactions: txs,
		Hash:         h.Hash,
	}
}

func computeTxRoot(txs []*Transaction) string {
	if len(txs) == 0 {
		return "0x" + hex.EncodeToString(make([]byte, 32))
	}
	h := sha256.New()
	for _, tx := range txs {
		h.Write([]byte(tx.Hash))
	}
	return "0x" + hex.EncodeToString(h.Sum(nil))
}

func estimateGasUsed(txs []*Transaction) uint64 {
	var total uint64
	for _, tx := range txs {
		total += tx.GasUsed
	}
	return total
}

func estimateBlockSize(txs []*Transaction) uint64 {
	base := uint64(500)
	for _, tx := range txs {
		b, _ := json.Marshal(tx)
		base += uint64(len(b))
	}
	return base
}

func (b *Block) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"number":       b.Header.Number,
		"hash":         b.Hash,
		"parentHash":   b.Header.ParentHash,
		"stateRoot":    b.Header.StateRoot,
		"txRoot":       b.Header.TxRoot,
		"receiptRoot":  b.Header.ReceiptRoot,
		"validator":    b.Header.Validator,
		"timestamp":    b.Header.Timestamp,
		"gasLimit":     b.Header.GasLimit,
		"gasUsed":      b.Header.GasUsed,
		"size":         b.Header.Size,
		"transactions": len(b.Transactions),
	}
}
