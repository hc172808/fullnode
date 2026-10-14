package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/cometbft/cometbft/crypto"
	cmtTypes "github.com/cometbft/cometbft/types"
)

const (
	ChainID          = "198281"
	AppVersion       = 1
	NativeDecimals   = 18
	NativeSupplyGYDS = int64(2_000_000_000)
)

type GenesisState struct {
	ChainID  string            `json:"chainId"`
	Balances map[string]string `json:"balances"`
}

func defaultGenesisState() GenesisState {
	return GenesisState{
		ChainID: ChainID,
		Balances: map[string]string{
			"0x0000000000000000000000000000000000000001": toBaseUnits(1_000_000_000),
			"0x0000000000000000000000000000000000000002": toBaseUnits(600_000_000),
			"0x0000000000000000000000000000000000000003": toBaseUnits(400_000_000),
		},
	}
}

func toBaseUnits(coins int64) string {
	units := new(big.Int).Exp(big.NewInt(10), big.NewInt(NativeDecimals), nil)
	return new(big.Int).Mul(big.NewInt(coins), units).String()
}

func validateGenesisState(state GenesisState) error {
	if state.ChainID != ChainID {
		return fmt.Errorf("prototype chain ID must be %s, got %q", ChainID, state.ChainID)
	}
	if len(state.Balances) == 0 {
		return fmt.Errorf("genesis must allocate the native supply")
	}
	total := new(big.Int)
	for address, amountText := range state.Balances {
		if len(address) != 42 || address[:2] != "0x" {
			return fmt.Errorf("invalid genesis address %q", address)
		}
		amount, ok := new(big.Int).SetString(amountText, 10)
		if !ok || amount.Sign() < 0 {
			return fmt.Errorf("invalid balance for %s", address)
		}
		total.Add(total, amount)
	}
	want := new(big.Int)
	want.SetString(toBaseUnits(NativeSupplyGYDS), 10)
	if total.Cmp(want) != 0 {
		return fmt.Errorf("genesis native supply is %s base units; want %s", total, want)
	}
	return nil
}

func makeGenesisDoc(chainID string, validator crypto.PubKey, state GenesisState) (*cmtTypes.GenesisDoc, error) {
	if chainID != ChainID {
		return nil, fmt.Errorf("CometBFT chain ID must be %s, got %q", ChainID, chainID)
	}
	if err := validateGenesisState(state); err != nil {
		return nil, err
	}
	appState, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode prototype app genesis: %w", err)
	}
	doc := &cmtTypes.GenesisDoc{
		GenesisTime:     time.Now().UTC(),
		ChainID:         ChainID,
		InitialHeight:   1,
		ConsensusParams: cmtTypes.DefaultConsensusParams(),
		Validators: []cmtTypes.GenesisValidator{{
			Address: validator.Address(),
			PubKey:  validator,
			Power:   1,
			Name:    "gyds-isolated-prototype",
		}},
		AppState: appState,
	}
	if err := doc.ValidateAndComplete(); err != nil {
		return nil, fmt.Errorf("validate prototype genesis: %w", err)
	}
	return doc, nil
}
