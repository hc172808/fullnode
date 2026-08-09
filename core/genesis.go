package core

import (
	"math/big"
	"time"
)

// GenesisAlloc sets the initial native GYDS balance for an address.
type GenesisAlloc struct {
	Address string   `json:"address"`
	Balance *big.Int `json:"balance"`
	Nonce   uint64   `json:"nonce"`
}

// GenesisTokenAlloc sets the initial balance of a non-native token for an address.
type GenesisTokenAlloc struct {
	Address string   `json:"address"`
	Amount  *big.Int `json:"amount"`
}

// TokenDefinition describes a token that exists in the genesis state.
type TokenDefinition struct {
	Symbol       string              `json:"symbol"`
	Name         string              `json:"name"`
	Decimals     uint8               `json:"decimals"`
	IsStablecoin bool                `json:"isStablecoin"`
	TotalSupply  *big.Int            `json:"totalSupply"`
	Alloc        []GenesisTokenAlloc `json:"alloc"`
}

type GenesisConfig struct {
	ChainID     int64             `json:"chainId"`
	NetworkName string            `json:"networkName"`
	Timestamp   int64             `json:"timestamp"`
	GasLimit    uint64            `json:"gasLimit"`
	Difficulty  *big.Int          `json:"difficulty"`
	ExtraData   string            `json:"extraData"`
	Validators  []string          `json:"validators"`
	Alloc       []GenesisAlloc    `json:"alloc"`
	Tokens      []TokenDefinition `json:"tokens"`
}

// e18 returns n × 10^18 (i.e. n whole tokens in wei).
func e18(n int64) *big.Int {
	return new(big.Int).Mul(
		big.NewInt(n),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
	)
}

// e18B returns n billion × 10^18.
func e18B(n int64) *big.Int { return e18(n * 1_000_000_000) }

// e18M returns n million × 10^18.
func e18M(n int64) *big.Int { return e18(n * 1_000_000) }

var GydsGenesis = &GenesisConfig{
	ChainID:     198282,
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
	// GYDS native token — 1,000,000,000 total supply
	Alloc: []GenesisAlloc{
		{Address: "0x0000000000000000000000000000000000000001", Balance: e18M(500)}, // 500 M GYDS
		{Address: "0x0000000000000000000000000000000000000002", Balance: e18M(300)}, // 300 M GYDS
		{Address: "0x0000000000000000000000000000000000000003", Balance: e18M(200)}, // 200 M GYDS
	},
	// Non-native genesis tokens
	Tokens: []TokenDefinition{
		{
			Symbol:       "GYD",
			Name:         "GYD Stablecoin",
			Decimals:     18,
			IsStablecoin: true,
			TotalSupply:  e18B(10), // 10,000,000,000 GYD
			Alloc: []GenesisTokenAlloc{
				{Address: "0x0000000000000000000000000000000000000001", Amount: e18B(5)}, // 5 B GYD
				{Address: "0x0000000000000000000000000000000000000002", Amount: e18B(3)}, // 3 B GYD
				{Address: "0x0000000000000000000000000000000000000003", Amount: e18B(2)}, // 2 B GYD
			},
		},
	},
}

// GydsTestGenesis is the genesis configuration for the isolated test node.
// It uses chain ID 31337 (0x7a69) — the industry-standard development chain ID
// recognised by MetaMask, Hardhat, Anvil, and most EVM tooling — so it is
// immediately distinguishable from the live GYDS network (198282 / 0x3068a).
// Data is wiped on every testnode start, so this genesis is always re-applied.
var GydsTestGenesis = &GenesisConfig{
	ChainID:     31337,
	NetworkName: "GYDS Test Network",
	Timestamp:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
	GasLimit:    30_000_000,
	Difficulty:  big.NewInt(1),
	ExtraData:   "0x4759445320546573744e6574776f726b",
	Validators: []string{
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
		"0x0000000000000000000000000000000000000003",
	},
	// Pre-fund test addresses with generous balances so tests never run dry.
	Alloc: []GenesisAlloc{
		{Address: "0x0000000000000000000000000000000000000001", Balance: e18M(500)},
		{Address: "0x0000000000000000000000000000000000000002", Balance: e18M(300)},
		{Address: "0x0000000000000000000000000000000000000003", Balance: e18M(200)},
	},
	Tokens: []TokenDefinition{
		{
			Symbol:       "GYD",
			Name:         "GYD Stablecoin",
			Decimals:     18,
			IsStablecoin: true,
			TotalSupply:  e18B(10),
			Alloc: []GenesisTokenAlloc{
				{Address: "0x0000000000000000000000000000000000000001", Amount: e18B(5)},
				{Address: "0x0000000000000000000000000000000000000002", Amount: e18B(3)},
				{Address: "0x0000000000000000000000000000000000000003", Amount: e18B(2)},
			},
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
