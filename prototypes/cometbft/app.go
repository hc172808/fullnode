package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/version"
	"github.com/ethereum/go-ethereum/common"
)

type appState struct {
	ChainID  string            `json:"chainId"`
	Height   int64             `json:"height"`
	Balances map[string]string `json:"balances"`
	Nonces   map[string]uint64 `json:"nonces"`
}

type app struct {
	abci.BaseApplication
	mu        sync.RWMutex
	state     appState
	pending   *appState
	stateFile string
}

var _ abci.Application = (*app)(nil)

func newApp(stateFile string) (*app, error) {
	a := &app{stateFile: stateFile}
	if stateFile == "" {
		return a, nil
	}
	data, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read prototype app state: %w", err)
	}
	if err := json.Unmarshal(data, &a.state); err != nil {
		return nil, fmt.Errorf("decode prototype app state: %w", err)
	}
	if a.state.Nonces == nil {
		a.state.Nonces = make(map[string]uint64)
	}
	if err := validateAppState(a.state); err != nil {
		return nil, fmt.Errorf("validate saved prototype state: %w", err)
	}
	return a, nil
}

func (a *app) Info(context.Context, *abci.RequestInfo) (*abci.ResponseInfo, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return &abci.ResponseInfo{
		Data:             "GYDS isolated CometBFT prototype; gasless EIP-155 native transfers only",
		Version:          version.ABCIVersion,
		AppVersion:       AppVersion,
		LastBlockHeight:  a.state.Height,
		LastBlockAppHash: stateHash(a.state),
	}, nil
}

func (a *app) InitChain(_ context.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	if req == nil || req.ChainId != ChainID {
		return nil, fmt.Errorf("unexpected CometBFT chain ID")
	}
	var genesis GenesisState
	if err := json.Unmarshal(req.AppStateBytes, &genesis); err != nil {
		return nil, fmt.Errorf("decode prototype genesis app state: %w", err)
	}
	if err := validateGenesisState(genesis); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	candidate := appState{
		ChainID:  genesis.ChainID,
		Balances: cloneBalances(genesis.Balances),
		Nonces:   make(map[string]uint64),
	}
	if a.state.Balances != nil {
		if !bytes.Equal(stateHash(a.state), stateHash(candidate)) {
			return nil, fmt.Errorf("saved app state does not match the prototype genesis")
		}
		candidate.Height = a.state.Height
	}
	a.state = candidate
	if err := a.persistLocked(a.state); err != nil {
		return nil, err
	}
	return &abci.ResponseInitChain{AppHash: stateHash(a.state)}, nil
}

func (a *app) Query(_ context.Context, req *abci.RequestQuery) (*abci.ResponseQuery, error) {
	if req == nil {
		return &abci.ResponseQuery{Code: 1, Log: "missing query"}, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	switch req.Path {
	case "/supply":
		total := totalSupply(a.state.Balances)
		result, _ := json.Marshal(map[string]string{
			"symbol":               "GYDS",
			"decimals":             strconv.Itoa(NativeDecimals),
			"totalSupply":          strconv.FormatInt(NativeSupplyGYDS, 10),
			"totalSupplyBaseUnits": total.String(),
		})
		return &abci.ResponseQuery{Code: 0, Value: result, Height: a.state.Height}, nil
	default:
		const prefix = "/balance/"
		if len(req.Path) > len(prefix) && req.Path[:len(prefix)] == prefix {
			address := req.Path[len(prefix):]
			if !common.IsHexAddress(address) {
				return &abci.ResponseQuery{Code: 1, Log: "invalid address"}, nil
			}
			balance, ok := a.state.Balances[addressKey(common.HexToAddress(address))]
			if !ok {
				balance = "0"
			}
			return &abci.ResponseQuery{Code: 0, Value: []byte(balance), Height: a.state.Height}, nil
		}
		const noncePrefix = "/nonce/"
		if len(req.Path) > len(noncePrefix) && req.Path[:len(noncePrefix)] == noncePrefix {
			address := req.Path[len(noncePrefix):]
			if !common.IsHexAddress(address) {
				return &abci.ResponseQuery{Code: 1, Log: "invalid address"}, nil
			}
			nonce := a.state.Nonces[addressKey(common.HexToAddress(address))]
			return &abci.ResponseQuery{
				Code:   0,
				Value:  []byte(strconv.FormatUint(nonce, 10)),
				Height: a.state.Height,
			}, nil
		}
		return &abci.ResponseQuery{Code: 1, Log: "supported paths: /supply, /balance/<address>, /nonce/<address>"}, nil
	}
}

func (a *app) CheckTx(_ context.Context, req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	if req == nil {
		return &abci.ResponseCheckTx{Code: 1, Log: "missing transaction"}, nil
	}
	a.mu.RLock()
	working := cloneState(a.state)
	a.mu.RUnlock()
	if err := applyNativeTransfer(&working, req.Tx); err != nil {
		return &abci.ResponseCheckTx{Code: 1, Codespace: "gyds-prototype", Log: err.Error()}, nil
	}
	return &abci.ResponseCheckTx{Code: 0}, nil
}

func (a *app) PrepareProposal(_ context.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	if req == nil || req.MaxTxBytes <= 0 {
		return &abci.ResponsePrepareProposal{Txs: [][]byte{}}, nil
	}
	a.mu.RLock()
	working := cloneState(a.state)
	a.mu.RUnlock()

	selected := make([][]byte, 0, len(req.Txs))
	var selectedBytes int64
	for _, raw := range req.Txs {
		txSize := int64(len(raw))
		if txSize > req.MaxTxBytes-selectedBytes {
			continue
		}
		candidate := cloneState(working)
		if err := applyNativeTransfer(&candidate, raw); err != nil {
			continue
		}
		working = candidate
		selected = append(selected, append([]byte(nil), raw...))
		selectedBytes += txSize
	}
	return &abci.ResponsePrepareProposal{Txs: selected}, nil
}

func (a *app) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	if req == nil {
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}
	a.mu.RLock()
	working := cloneState(a.state)
	a.mu.RUnlock()
	for _, raw := range req.Txs {
		if err := applyNativeTransfer(&working, raw); err != nil {
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
	}
	if working.ChainID != ChainID {
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
}

func (a *app) FinalizeBlock(_ context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	if req == nil {
		return nil, fmt.Errorf("missing finalized block")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Balances == nil {
		return nil, fmt.Errorf("application state is not initialized")
	}
	if a.pending != nil {
		return nil, fmt.Errorf("previous block has not been committed")
	}
	if req.Height != a.state.Height+1 {
		return nil, fmt.Errorf("unexpected block height %d; expected %d", req.Height, a.state.Height+1)
	}
	next := cloneState(a.state)
	next.Height = req.Height
	results := make([]*abci.ExecTxResult, len(req.Txs))
	for i, raw := range req.Txs {
		if err := applyNativeTransfer(&next, raw); err != nil {
			results[i] = &abci.ExecTxResult{Code: 1, Codespace: "gyds-prototype", Log: err.Error()}
			continue
		}
		results[i] = &abci.ExecTxResult{
			Code:      0,
			GasWanted: int64(nativeTransferGasLimit),
			GasUsed:   int64(nativeTransferGasLimit),
		}
	}
	a.pending = &next
	return &abci.ResponseFinalizeBlock{TxResults: results, AppHash: stateHash(next)}, nil
}

func (a *app) Commit(context.Context, *abci.RequestCommit) (*abci.ResponseCommit, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		return &abci.ResponseCommit{}, nil
	}
	if err := a.persistLocked(*a.pending); err != nil {
		return nil, err
	}
	a.state = *a.pending
	a.pending = nil
	return &abci.ResponseCommit{}, nil
}

func (a *app) persistLocked(state appState) error {
	if a.stateFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(a.stateFile), 0o700); err != nil {
		return fmt.Errorf("create prototype app-state directory: %w", err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode prototype app state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.stateFile), ".app-state-*")
	if err != nil {
		return fmt.Errorf("create prototype app-state temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write prototype app state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync prototype app state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close prototype app state: %w", err)
	}
	if err := os.Rename(tmpName, a.stateFile); err != nil {
		return fmt.Errorf("commit prototype app state: %w", err)
	}
	return nil
}

func cloneState(state appState) appState {
	return appState{
		ChainID:  state.ChainID,
		Height:   state.Height,
		Balances: cloneBalances(state.Balances),
		Nonces:   cloneNonces(state.Nonces),
	}
}

func cloneBalances(balances map[string]string) map[string]string {
	copyOf := make(map[string]string, len(balances))
	for address, amount := range balances {
		copyOf[address] = amount
	}
	return copyOf
}

func cloneNonces(nonces map[string]uint64) map[string]uint64 {
	copyOf := make(map[string]uint64, len(nonces))
	for address, nonce := range nonces {
		copyOf[address] = nonce
	}
	return copyOf
}

func validateAppState(state appState) error {
	if state.ChainID != ChainID || state.Height < 0 {
		return fmt.Errorf("incompatible chain ID or height")
	}
	if err := validateGenesisState(GenesisState{ChainID: state.ChainID, Balances: state.Balances}); err != nil {
		return err
	}
	for address := range state.Nonces {
		if !common.IsHexAddress(address) || address != strings.ToLower(address) {
			return fmt.Errorf("invalid nonce address %q", address)
		}
	}
	return nil
}

func totalSupply(balances map[string]string) *big.Int {
	total := new(big.Int)
	for _, value := range balances {
		if amount, ok := new(big.Int).SetString(value, 10); ok {
			total.Add(total, amount)
		}
	}
	return total
}

func stateHash(state appState) []byte {
	if state.Balances == nil {
		return nil
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(state.ChainID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(state.Height, 10)))
	_, _ = hash.Write([]byte{0})
	addressSet := make(map[string]struct{}, len(state.Balances)+len(state.Nonces))
	for address := range state.Balances {
		addressSet[address] = struct{}{}
	}
	for address := range state.Nonces {
		addressSet[address] = struct{}{}
	}
	addresses := make([]string, 0, len(addressSet))
	for address := range addressSet {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		_, _ = hash.Write([]byte(address))
		_, _ = hash.Write([]byte{0})
		balance := state.Balances[address]
		if balance == "" {
			balance = "0"
		}
		_, _ = hash.Write([]byte(balance))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strconv.FormatUint(state.Nonces[address], 10)))
		_, _ = hash.Write([]byte{0})
	}
	return hash.Sum(nil)
}

func hashString(state appState) string {
	return hex.EncodeToString(stateHash(state))
}
