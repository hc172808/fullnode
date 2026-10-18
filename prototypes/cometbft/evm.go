package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethcore "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

const (
	prototypeBlockGasLimit = uint64(30_000_000)
	prototypeMaxTxCount    = 4096
)

type evmAccountState struct {
	Code    string            `json:"code,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
}

type prototypeBlockContext struct {
	Number    uint64
	Timestamp uint64
	Hash      common.Hash
}

type prototypeTxReceipt struct {
	TransactionHash string           `json:"transactionHash"`
	Status          uint64           `json:"status"`
	BlockNumber     uint64           `json:"blockNumber"`
	Transaction     uint32           `json:"transactionIndex"`
	GasUsed         uint64           `json:"gasUsed"`
	ContractAddress string           `json:"contractAddress,omitempty"`
	ReturnData      string           `json:"returnData,omitempty"`
	Logs            []prototypeLog   `json:"logs,omitempty"`
}

type prototypeLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type prototypeTxExecution struct {
	GasWanted int64
	GasUsed   int64
	Data      []byte
	Events    []abci.Event
}

func decodePrototypeTransaction(raw []byte) (*types.Transaction, common.Address, error) {
	if len(raw) == 0 || len(raw) > maxPrototypeTxBytes {
		return nil, common.Address{}, fmt.Errorf("transaction size must be between 1 and %d bytes", maxPrototypeTxBytes)
	}

	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nil, common.Address{}, fmt.Errorf("decode Ethereum transaction: %w", err)
	}
	if tx.Type() > types.DynamicFeeTxType {
		return nil, common.Address{}, fmt.Errorf("prototype supports only legacy, access-list, and EIP-1559 transactions")
	}
	if tx.Type() == types.LegacyTxType && !tx.Protected() {
		return nil, common.Address{}, errors.New("legacy transaction must be EIP-155 protected")
	}
	chainID, ok := new(big.Int).SetString(ChainID, 10)
	if !ok || tx.ChainId().Cmp(chainID) != 0 {
		return nil, common.Address{}, fmt.Errorf("transaction must use prototype chain ID %s", ChainID)
	}

	canonical, err := tx.MarshalBinary()
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("encode canonical transaction: %w", err)
	}
	if !bytes.Equal(canonical, raw) {
		return nil, common.Address{}, errors.New("transaction encoding is not canonical")
	}
	if tx.GasPrice() == nil || tx.GasPrice().Sign() != 0 ||
		tx.GasFeeCap().Sign() != 0 || tx.GasTipCap().Sign() != 0 {
		return nil, common.Address{}, errors.New("prototype transactions must have zero gas fees")
	}

	sender, err := types.Sender(types.LatestSignerForChainID(chainID), &tx)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("recover transaction signer: %w", err)
	}
	return &tx, sender, nil
}

func applyPrototypeTransaction(
	appState *appState,
	raw []byte,
	block prototypeBlockContext,
	txIndex uint32,
	remainingGas uint64,
) (*prototypeTxExecution, error) {
	if appState == nil || appState.ChainID != ChainID || appState.Balances == nil {
		return nil, fmt.Errorf("application state is not initialized for chain %s", ChainID)
	}
	if remainingGas == 0 {
		return nil, errors.New("prototype block gas limit is exhausted")
	}
	tx, sender, err := decodePrototypeTransaction(raw)
	if err != nil {
		return nil, err
	}
	if tx.Gas() > remainingGas {
		return nil, fmt.Errorf("transaction gas limit %d exceeds remaining block gas %d", tx.Gas(), remainingGas)
	}

	working := cloneState(*appState)
	stateDB, chainConfig, err := loadPrototypeEVMState(working)
	if err != nil {
		return nil, err
	}
	number := new(big.Int).SetUint64(block.Number)
	rules := chainConfig.Rules(number, true, block.Timestamp)
	baseFee := new(big.Int)
	message, err := gethcore.TransactionToMessage(tx, types.LatestSignerForChainID(chainConfig.ChainID), baseFee)
	if err != nil {
		return nil, fmt.Errorf("derive transaction message: %w", err)
	}

	gasPool := new(gethcore.GasPool).AddGas(remainingGas)
	stateDB.SetTxContext(tx.Hash(), int(txIndex), txIndex)
	stateDB.Prepare(rules, sender, common.Address{}, tx.To(), vm.ActivePrecompiles(rules), tx.AccessList())

	zeroHash := common.Hash{}
	blockContext := vm.BlockContext{
		CanTransfer: gethcore.CanTransfer,
		Transfer:    gethcore.Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		GasLimit:    prototypeBlockGasLimit,
		BlockNumber: number,
		Time:        block.Timestamp,
		Difficulty:  new(big.Int),
		BaseFee:     baseFee,
		BlobBaseFee: new(big.Int),
		Random:      &zeroHash,
	}
	execution := vm.NewEVM(blockContext, stateDB, chainConfig, vm.Config{})
	execution.SetTxContext(gethcore.NewEVMTxContext(message))
	result, err := gethcore.ApplyMessage(execution, message, gasPool)
	if err != nil {
		return nil, fmt.Errorf("execute transaction %s: %w", tx.Hash(), err)
	}
	if result.UsedGas > remainingGas {
		return nil, errors.New("transaction exceeded remaining block gas")
	}

	stateDB.Finalise(rules)
	logs := stateDB.GetLogs(tx.Hash(), block.Number, block.Hash, block.Timestamp)
	root, err := stateDB.Commit(rules, block.Number)
	if err != nil {
		return nil, fmt.Errorf("commit prototype EVM state: %w", err)
	}
	dump := stateDB.RawDump(nil)
	if err := exportPrototypeEVMState(&working, root, dump.Accounts); err != nil {
		return nil, err
	}

	status := uint64(types.ReceiptStatusSuccessful)
	if result.Failed() {
		status = types.ReceiptStatusFailed
	}
	receipt := prototypeTxReceipt{
		TransactionHash: tx.Hash().Hex(),
		Status:          status,
		BlockNumber:     block.Number,
		Transaction:     txIndex,
		GasUsed:         result.UsedGas,
	}
	if tx.To() == nil && !result.Failed() {
		receipt.ContractAddress = crypto.CreateAddress(sender, tx.Nonce()).Hex()
	}
	if len(result.ReturnData) > 0 {
		receipt.ReturnData = hexutil.Encode(result.ReturnData)
	}
	for _, entry := range logs {
		topics := make([]string, len(entry.Topics))
		for i, topic := range entry.Topics {
			topics[i] = topic.Hex()
		}
		receipt.Logs = append(receipt.Logs, prototypeLog{
			Address: entry.Address.Hex(),
			Topics:  topics,
			Data:    hexutil.Encode(entry.Data),
		})
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode prototype transaction receipt: %w", err)
	}

	*appState = working
	return &prototypeTxExecution{
		GasWanted: int64(tx.Gas()),
		GasUsed:   int64(result.UsedGas),
		Data:      data,
	}, nil
}

func loadPrototypeEVMState(appState appState) (*state.StateDB, *params.ChainConfig, error) {
	chainID, ok := new(big.Int).SetString(ChainID, 10)
	if !ok {
		return nil, nil, errors.New("invalid prototype EVM chain ID")
	}
	config := *params.AllDevChainProtocolChanges
	config.ChainID = chainID

	stateDB, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		return nil, nil, fmt.Errorf("create prototype EVM state: %w", err)
	}
	addresses := make(map[string]struct{}, len(appState.Balances)+len(appState.Nonces)+len(appState.EVMAccounts))
	for address := range appState.Balances {
		addresses[address] = struct{}{}
	}
	for address := range appState.Nonces {
		addresses[address] = struct{}{}
	}
	for address := range appState.EVMAccounts {
		addresses[address] = struct{}{}
	}
	ordered := make([]string, 0, len(addresses))
	for address := range addresses {
		ordered = append(ordered, address)
	}
	sort.Strings(ordered)

	for _, addressText := range ordered {
		if !common.IsHexAddress(addressText) || addressText != strings.ToLower(addressText) {
			return nil, nil, fmt.Errorf("invalid prototype EVM account address %q", addressText)
		}
		address := common.HexToAddress(addressText)
		balance := new(big.Int)
		if balanceText := appState.Balances[addressText]; balanceText != "" {
			if _, ok := balance.SetString(balanceText, 10); !ok || balance.Sign() < 0 || balance.BitLen() > 256 {
				return nil, nil, fmt.Errorf("invalid EVM balance for %s", addressText)
			}
		}
		balance256, overflow := uint256.FromBig(balance)
		if overflow {
			return nil, nil, fmt.Errorf("EVM balance exceeds uint256 for %s", addressText)
		}
		stateDB.SetBalance(address, balance256, tracing.BalanceChangeUnspecified)
		stateDB.SetNonce(address, appState.Nonces[addressText], tracing.NonceChangeUnspecified)

		account, exists := appState.EVMAccounts[addressText]
		if !exists {
			continue
		}
		if account.Code != "" {
			code, err := hexutil.Decode(account.Code)
			if err != nil || hexutil.Encode(code) != account.Code {
				return nil, nil, fmt.Errorf("invalid or non-canonical EVM code for %s", addressText)
			}
			stateDB.SetCode(address, code, tracing.CodeChangeUnspecified)
		}
		for slotText, valueText := range account.Storage {
			slot, err := decodeCanonicalHash(slotText)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid EVM storage slot for %s: %w", addressText, err)
			}
			value, err := decodeCanonicalHash(valueText)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid EVM storage value for %s: %w", addressText, err)
			}
			stateDB.SetState(address, slot, value)
		}
	}
	return stateDB, &config, nil
}

func exportPrototypeEVMState(appState *appState, root common.Hash, dumped map[string]state.DumpAccount) error {
	balances := make(map[string]string, len(dumped))
	nonces := make(map[string]uint64, len(dumped))
	contracts := make(map[string]evmAccountState)
	for key, account := range dumped {
		if account.Address == nil {
			return fmt.Errorf("cannot export EVM account without its original address (%s)", key)
		}
		address := addressKey(*account.Address)
		balances[address] = account.Balance
		nonces[address] = account.Nonce

		contract := evmAccountState{}
		if len(account.Code) > 0 {
			contract.Code = hexutil.Encode(account.Code)
		}
		if len(account.Storage) > 0 {
			contract.Storage = make(map[string]string, len(account.Storage))
			for slot, valueText := range account.Storage {
				value := common.HexToHash(valueText)
				if value == (common.Hash{}) {
					continue
				}
				contract.Storage[slot.Hex()] = value.Hex()
			}
		}
		if contract.Code != "" || len(contract.Storage) > 0 {
			contracts[address] = contract
		}
	}
	appState.Balances = balances
	appState.Nonces = nonces
	appState.EVMAccounts = contracts
	appState.EVMRoot = root.Hex()
	return nil
}

func prototypeEVMRoot(appState appState) (common.Hash, error) {
	stateDB, config, err := loadPrototypeEVMState(appState)
	if err != nil {
		return common.Hash{}, err
	}
	number := uint64(appState.Height)
	rules := config.Rules(new(big.Int).SetUint64(number), true, 0)
	root, err := stateDB.Commit(rules, number)
	if err != nil {
		return common.Hash{}, fmt.Errorf("compute prototype EVM state root: %w", err)
	}
	return root, nil
}

func decodeCanonicalHash(value string) (common.Hash, error) {
	if len(value) != 2+common.HashLength*2 || !strings.HasPrefix(value, "0x") {
		return common.Hash{}, errors.New("value must be a 32-byte 0x-prefixed hash")
	}
	decoded, err := hexutil.Decode(value)
	if err != nil {
		return common.Hash{}, err
	}
	hash := common.BytesToHash(decoded)
	if hash.Hex() != value {
		return common.Hash{}, errors.New("hash is not canonically encoded")
	}
	return hash, nil
}

func prototypeABCIResult(execution *prototypeTxExecution) *abci.ExecTxResult {
	if execution == nil {
		return &abci.ExecTxResult{Code: 1, Codespace: "gyds-prototype", Log: "transaction execution returned no result"}
	}
	return &abci.ExecTxResult{
		Code:      0,
		Data:      bytes.Clone(execution.Data),
		GasWanted: execution.GasWanted,
		GasUsed:   execution.GasUsed,
	}
}
