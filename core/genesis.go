package core

import (
        "math/big"
        "time"
)

type GenesisAlloc struct {
        Address string   `json:"address"`
        Balance *big.Int `json:"balance"`
        Nonce   uint64   `json:"nonce"`
}

type GenesisConfig struct {
        ChainID     int64          `json:"chainId"`
        NetworkName string         `json:"networkName"`
        Timestamp   int64          `json:"timestamp"`
        GasLimit    uint64         `json:"gasLimit"`
        Difficulty  *big.Int       `json:"difficulty"`
        ExtraData   string         `json:"extraData"`
        Validators  []string       `json:"validators"`
        Alloc       []GenesisAlloc `json:"alloc"`
}

var GydsGenesis = &GenesisConfig{
        ChainID:     13370,
        NetworkName: "GYDS Chain",
        Timestamp:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
        GasLimit:    30_000_000,
        Difficulty:  big.NewInt(1),
        ExtraData:   "0x4759445320436861696e202d20476f7920446563656e7472616c697a656420536f6c7574696f6e73",
        Validators: []string{
                "0x0000000000000000000000000000000000000001",
                "0x0000000000000000000000000000000000000002",
                "0x0000000000000000000000000000000000000003",
        },
        Alloc: []GenesisAlloc{
                {
                        Address: "0x0000000000000000000000000000000000000001",
                        Balance: new(big.Int).Mul(big.NewInt(100_000_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)),
                },
                {
                        Address: "0x0000000000000000000000000000000000000002",
                        Balance: new(big.Int).Mul(big.NewInt(50_000_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)),
                },
        },
}

func GenesisBlock(cfg *GenesisConfig) *Block {
        if cfg == nil {
                cfg = GydsGenesis
        }
        h := &Header{
                Number:      0,
                ParentHash:  "0x0000000000000000000000000000000000000000000000000000000000000000",
                StateRoot:   "0x" + "0000000000000000000000000000000000000000000000000000000000000000",
                TxRoot:      "0x" + "0000000000000000000000000000000000000000000000000000000000000000",
                ReceiptRoot: "0x" + "0000000000000000000000000000000000000000000000000000000000000000",
                Validator:   cfg.Validators[0],
                Timestamp:   cfg.Timestamp,
                GasLimit:    cfg.GasLimit,
                GasUsed:     0,
                Difficulty:  cfg.Difficulty,
                Size:        512,
        }
        h.Hash = h.ComputeHash()
        return &Block{Header: h, Transactions: nil, Hash: h.Hash}
}
