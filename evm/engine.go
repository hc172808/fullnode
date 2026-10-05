package evm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	gethcore "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	gethleveldb "github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

const (
	stateMetaKey      = "gyds:evm:state-meta:v1"
	maxRawTransaction = 128 * 1024
	maxBlockTxs       = 4096
)

var (
	ErrInvalidChainID       = errors.New("evm: invalid chain id")
	ErrWrongChainID         = errors.New("evm: transaction chain id does not match this chain")
	ErrUnsupportedTxType    = errors.New("evm: unsupported transaction type")
	ErrInvalidBlockNumber   = errors.New("evm: block number is not the next state transition")
	ErrInvalidBlockGasLimit = errors.New("evm: block gas limit must be positive")
)

// GenesisAccount is the EVM-compatible portion of an account at the point
// where EVM execution is initialized.
type GenesisAccount struct {
	Balance *big.Int
	Nonce   uint64
}

// BlockContext supplies consensus-derived values needed by EVM execution.
// GetHash should return canonical ancestor hashes for BLOCKHASH. A nil callback
// returns the zero hash and is suitable only for isolated tests.
type BlockContext struct {
	Number      uint64
	Timestamp   uint64
	GasLimit    uint64
	Coinbase    common.Address
	BaseFee     *big.Int
	BlobBaseFee *big.Int
	Difficulty  *big.Int
	Random      *common.Hash
	GetHash     func(uint64) common.Hash
}

type BlockResult struct {
	StateRoot       common.Hash
	TransactionRoot common.Hash
	ReceiptRoot     common.Hash
	GasUsed         uint64
	Receipts        types.Receipts
}

type stateMetadata struct {
	Version   uint64 `json:"version"`
	ChainID   string `json:"chainId"`
	Height    uint64 `json:"height"`
	StateRoot string `json:"stateRoot"`
}

// Engine executes signed EVM transactions against a persistent Ethereum-style
// state trie. It is deliberately separate from the legacy chain state until
// block production and validation are wired to this engine.
type Engine struct {
	mu sync.RWMutex

	chainID *big.Int
	config  *params.ChainConfig

	kvdb         ethdb.KeyValueStore
	closeKV      func() error
	trieDB       *triedb.Database
	stateDBStore state.Database
	stateDB      *state.StateDB

	root   common.Hash
	height uint64
	closed bool
}

// Open creates or opens the EVM state database under dataDir/evm-state.
// Existing EVM state is preserved and must match chainID; it is never reset
// when metadata is malformed or belongs to another chain.
func Open(dataDir string, chainID *big.Int, alloc map[common.Address]GenesisAccount) (*Engine, error) {
	return OpenAt(dataDir, chainID, 0, alloc)
}

// OpenAt creates or opens the EVM state database and assigns initialHeight to a
// newly created state snapshot. Use this only at a coordinated activation
// boundary after the legacy balances have been loaded.
func OpenAt(dataDir string, chainID *big.Int, initialHeight uint64, alloc map[common.Address]GenesisAccount) (*Engine, error) {
	if dataDir == "" {
		return nil, errors.New("evm: data directory is required")
	}
	if err := validateChainID(chainID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("evm: create data directory: %w", err)
	}
	db, err := gethleveldb.New(filepath.Join(dataDir, "evm-state"), 64, 16, "gyds/evm", false)
	if err != nil {
		return nil, fmt.Errorf("evm: open state database: %w", err)
	}
	engine, err := newEngine(db, db.Close, chainID, initialHeight, alloc, true)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return engine, nil
}

// OpenExisting opens an already initialized EVM state database. It does not
// seed or replace missing state, which prevents a restart from silently
// reconstructing a different chain state.
func OpenExisting(dataDir string, chainID *big.Int) (*Engine, error) {
	if dataDir == "" {
		return nil, errors.New("evm: data directory is required")
	}
	if err := validateChainID(chainID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("evm: create data directory: %w", err)
	}
	db, err := gethleveldb.New(filepath.Join(dataDir, "evm-state"), 64, 16, "gyds/evm", false)
	if err != nil {
		return nil, fmt.Errorf("evm: open state database: %w", err)
	}
	engine, err := newEngine(db, db.Close, chainID, 0, nil, false)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return engine, nil
}

// NewMemory creates an isolated in-memory engine, primarily for tests and
// simulations.
func NewMemory(chainID *big.Int, alloc map[common.Address]GenesisAccount) (*Engine, error) {
	return NewMemoryAt(chainID, 0, alloc)
}

// NewMemoryAt is the in-memory equivalent of OpenAt.
func NewMemoryAt(chainID *big.Int, initialHeight uint64, alloc map[common.Address]GenesisAccount) (*Engine, error) {
	if err := validateChainID(chainID); err != nil {
		return nil, err
	}
	return newEngine(memorydb.New(), nil, chainID, initialHeight, alloc, true)
}

func newEngine(kvdb ethdb.KeyValueStore, closeKV func() error, chainID *big.Int, initialHeight uint64, alloc map[common.Address]GenesisAccount, allowCreate bool) (*Engine, error) {
	config := *params.AllEthashProtocolChanges
	config.ChainID = new(big.Int).Set(chainID)
	shanghaiTime, cancunTime := uint64(0), uint64(0)
	config.ShanghaiTime = &shanghaiTime
	config.CancunTime = &cancunTime

	diskDB := rawdb.NewDatabase(kvdb)
	trieDB := triedb.NewDatabase(diskDB, triedb.HashDefaults)
	stateDBStore := state.NewDatabase(trieDB, state.NewCodeDB(kvdb))
	engine := &Engine{
		chainID:      new(big.Int).Set(chainID),
		config:       &config,
		kvdb:         kvdb,
		closeKV:      closeKV,
		trieDB:       trieDB,
		stateDBStore: stateDBStore,
	}

	hasMeta, err := kvdb.Has([]byte(stateMetaKey))
	if err != nil {
		_ = trieDB.Close()
		return nil, fmt.Errorf("evm: read state metadata: %w", err)
	}
	if hasMeta {
		if err := engine.loadExistingState(); err != nil {
			_ = trieDB.Close()
			return nil, err
		}
		return engine, nil
	}
	if !allowCreate {
		_ = trieDB.Close()
		return nil, errors.New("evm: state database is not initialized")
	}

	statedb, err := state.New(types.EmptyRootHash, stateDBStore)
	if err != nil {
		_ = trieDB.Close()
		return nil, fmt.Errorf("evm: create initial state: %w", err)
	}
	for addr, account := range alloc {
		balance := account.Balance
		if balance == nil {
			balance = new(big.Int)
		}
		if balance.Sign() < 0 {
			_ = trieDB.Close()
			return nil, fmt.Errorf("evm: negative genesis balance for %s", addr.Hex())
		}
		value, overflow := uint256.FromBig(balance)
		if overflow {
			_ = trieDB.Close()
			return nil, fmt.Errorf("evm: genesis balance exceeds 256 bits for %s", addr.Hex())
		}
		statedb.SetBalance(addr, value, 0)
		statedb.SetNonce(addr, account.Nonce, 0)
	}
	root, err := statedb.Commit(config.Rules(new(big.Int).SetUint64(initialHeight), true, 0), initialHeight)
	if err != nil {
		_ = trieDB.Close()
		return nil, fmt.Errorf("evm: commit initial state: %w", err)
	}
	if err := trieDB.Commit(root, false); err != nil {
		_ = trieDB.Close()
		return nil, fmt.Errorf("evm: flush initial state: %w", err)
	}
	initialState, err := state.New(root, stateDBStore)
	if err != nil {
		_ = trieDB.Close()
		return nil, fmt.Errorf("evm: reopen initial state: %w", err)
	}
	engine.root = root
	engine.height = initialHeight
	engine.stateDB = initialState
	if err := engine.writeMetadata(root, initialHeight); err != nil {
		_ = trieDB.Close()
		return nil, fmt.Errorf("evm: persist initial state metadata: %w", err)
	}
	return engine, nil
}

func validateChainID(chainID *big.Int) error {
	if chainID == nil || chainID.Sign() <= 0 || chainID.BitLen() > 256 {
		return ErrInvalidChainID
	}
	return nil
}

func (e *Engine) loadExistingState() error {
	raw, err := e.kvdb.Get([]byte(stateMetaKey))
	if err != nil {
		return fmt.Errorf("evm: read state metadata: %w", err)
	}
	var meta stateMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("evm: invalid state metadata: %w", err)
	}
	if meta.Version != 1 {
		return fmt.Errorf("evm: unsupported state metadata version %d", meta.Version)
	}
	storedChainID, ok := new(big.Int).SetString(meta.ChainID, 10)
	if !ok || storedChainID.Cmp(e.chainID) != 0 {
		return fmt.Errorf("evm: state database chain id %q does not match %s", meta.ChainID, e.chainID)
	}
	rootBytes := common.FromHex(meta.StateRoot)
	if len(rootBytes) != common.HashLength {
		return errors.New("evm: invalid state root in metadata")
	}
	root := common.BytesToHash(rootBytes)
	statedb, err := state.New(root, e.stateDBStore)
	if err != nil {
		return fmt.Errorf("evm: open persisted state at height %d: %w", meta.Height, err)
	}
	e.root = root
	e.height = meta.Height
	e.stateDB = statedb
	return nil
}

func (e *Engine) writeMetadata(root common.Hash, height uint64) error {
	raw, err := json.Marshal(stateMetadata{
		Version:   1,
		ChainID:   e.chainID.String(),
		Height:    height,
		StateRoot: root.Hex(),
	})
	if err != nil {
		return err
	}
	return e.kvdb.Put([]byte(stateMetaKey), raw)
}

// DecodeSignedTransaction decodes canonical Ethereum transaction bytes,
// enforces the configured chain ID, and recovers the sender from the signature.
// Legacy unprotected and blob transactions are rejected by this first execution
// implementation; legacy EIP-155, access-list, and EIP-1559 transactions are
// supported.
func DecodeSignedTransaction(raw []byte, chainID *big.Int) (*types.Transaction, common.Address, error) {
	if err := validateChainID(chainID); err != nil {
		return nil, common.Address{}, err
	}
	if len(raw) == 0 || len(raw) > maxRawTransaction {
		return nil, common.Address{}, errors.New("evm: raw transaction has invalid length")
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nil, common.Address{}, fmt.Errorf("evm: decode raw transaction: %w", err)
	}
	if tx.Type() > types.DynamicFeeTxType {
		return nil, common.Address{}, fmt.Errorf("%w: %d", ErrUnsupportedTxType, tx.Type())
	}
	if tx.ChainId().Cmp(chainID) != 0 {
		return nil, common.Address{}, ErrWrongChainID
	}
	encoded, err := tx.MarshalBinary()
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("evm: re-encode transaction: %w", err)
	}
	if !bytes.Equal(encoded, raw) {
		return nil, common.Address{}, errors.New("evm: raw transaction is not canonically encoded")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(chainID), &tx)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("evm: recover transaction sender: %w", err)
	}
	return &tx, sender, nil
}

// ApplyBlock executes an ordered transaction list atomically against the
// current EVM state. Any invalid transaction rejects the whole block without
// advancing the state root or height.
func (e *Engine) ApplyBlock(block BlockContext, txs []*types.Transaction) (BlockResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return BlockResult{}, errors.New("evm: engine is closed")
	}
	if block.Number != e.height+1 {
		return BlockResult{}, fmt.Errorf("%w: got %d, want %d", ErrInvalidBlockNumber, block.Number, e.height+1)
	}
	if block.GasLimit == 0 {
		return BlockResult{}, ErrInvalidBlockGasLimit
	}
	if len(txs) > maxBlockTxs {
		return BlockResult{}, fmt.Errorf("evm: block contains too many transactions: %d", len(txs))
	}

	baseFee := new(big.Int)
	if block.BaseFee != nil {
		if block.BaseFee.Sign() < 0 {
			return BlockResult{}, errors.New("evm: negative base fee")
		}
		baseFee.Set(block.BaseFee)
	}
	blobBaseFee := new(big.Int)
	if block.BlobBaseFee != nil {
		if block.BlobBaseFee.Sign() < 0 {
			return BlockResult{}, errors.New("evm: negative blob base fee")
		}
		blobBaseFee.Set(block.BlobBaseFee)
	}
	difficulty := new(big.Int)
	if block.Difficulty != nil {
		if block.Difficulty.Sign() < 0 {
			return BlockResult{}, errors.New("evm: negative difficulty")
		}
		difficulty.Set(block.Difficulty)
	}

	number := new(big.Int).SetUint64(block.Number)
	rules := e.config.Rules(number, true, block.Timestamp)
	working := e.stateDB.Copy()
	gasPool := gethcore.NewGasPool(block.GasLimit)
	receipts := make(types.Receipts, 0, len(txs))
	var cumulativeGas uint64

	for index, tx := range txs {
		if tx == nil {
			return BlockResult{}, fmt.Errorf("evm: nil transaction at index %d", index)
		}
		if tx.Type() > types.DynamicFeeTxType {
			return BlockResult{}, fmt.Errorf("%w: %d", ErrUnsupportedTxType, tx.Type())
		}
		if tx.ChainId().Cmp(e.chainID) != 0 {
			return BlockResult{}, fmt.Errorf("%w: transaction %s", ErrWrongChainID, tx.Hash())
		}
		if uint64(index) > uint64(^uint32(0)) {
			return BlockResult{}, errors.New("evm: transaction index exceeds EVM limit")
		}

		signer := types.LatestSignerForChainID(e.chainID)
		message, err := gethcore.TransactionToMessage(tx, signer, baseFee)
		if err != nil {
			return BlockResult{}, fmt.Errorf("evm: derive transaction message %s: %w", tx.Hash(), err)
		}

		working.SetTxContext(tx.Hash(), index, uint32(index))
		working.Prepare(rules, message.From, block.Coinbase, message.To, vm.ActivePrecompiles(rules), tx.AccessList())

		random := common.Hash{}
		if block.Random != nil {
			random = *block.Random
		}
		getHash := block.GetHash
		if getHash == nil {
			getHash = func(uint64) common.Hash { return common.Hash{} }
		}
		blockContext := vm.BlockContext{
			CanTransfer: gethcore.CanTransfer,
			Transfer:    gethcore.Transfer,
			GetHash:     getHash,
			Coinbase:    block.Coinbase,
			GasLimit:    block.GasLimit,
			BlockNumber: new(big.Int).Set(number),
			Time:        block.Timestamp,
			Difficulty:  difficulty,
			BaseFee:     baseFee,
			BlobBaseFee: blobBaseFee,
			Random:      &random,
		}
		evm := vm.NewEVM(blockContext, working, e.config, vm.Config{})
		evm.SetTxContext(gethcore.NewEVMTxContext(message))
		result, err := gethcore.ApplyMessage(evm, message, gasPool)
		if err != nil {
			return BlockResult{}, fmt.Errorf("evm: reject transaction %s: %w", tx.Hash(), err)
		}
		working.Finalise(rules)
		if result.UsedGas > block.GasLimit-cumulativeGas {
			return BlockResult{}, errors.New("evm: cumulative gas exceeds block gas limit")
		}
		cumulativeGas += result.UsedGas

		status := types.ReceiptStatusSuccessful
		if result.Failed() {
			status = types.ReceiptStatusFailed
		}
		receipt := &types.Receipt{
			Type:              tx.Type(),
			Status:            status,
			CumulativeGasUsed: cumulativeGas,
			TxHash:            tx.Hash(),
			GasUsed:           result.UsedGas,
			EffectiveGasPrice: message.GasPrice.ToBig(),
			BlockNumber:       new(big.Int).Set(number),
			TransactionIndex:  uint(index),
			Logs:              working.GetLogs(tx.Hash(), block.Number, common.Hash{}, block.Timestamp),
		}
		if tx.To() == nil && !result.Failed() {
			receipt.ContractAddress = crypto.CreateAddress(message.From, tx.Nonce())
		}
		receipt.Bloom = types.CreateBloom(receipt)
		receipts = append(receipts, receipt)
	}

	transactionRoot := types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil))
	receiptRoot := types.DeriveSha(receipts, trie.NewStackTrie(nil))
	root, err := working.Commit(rules, block.Number)
	if err != nil {
		return BlockResult{}, fmt.Errorf("evm: commit block state: %w", err)
	}
	if err := e.trieDB.Commit(root, false); err != nil {
		return BlockResult{}, fmt.Errorf("evm: flush block state: %w", err)
	}
	nextState, err := state.New(root, e.stateDBStore)
	if err != nil {
		return BlockResult{}, fmt.Errorf("evm: reopen committed block state: %w", err)
	}
	if err := e.writeMetadata(root, block.Number); err != nil {
		return BlockResult{}, fmt.Errorf("evm: persist block state metadata: %w", err)
	}

	e.root = root
	e.height = block.Number
	e.stateDB = nextState
	return BlockResult{
		StateRoot:       root,
		TransactionRoot: transactionRoot,
		ReceiptRoot:     receiptRoot,
		GasUsed:         cumulativeGas,
		Receipts:        receipts,
	}, nil
}

// Call executes a read-only RPC call against a copied state snapshot.
// The copy is discarded, so storage writes and nonce changes are never saved.
func (e *Engine) Call(block BlockContext, from common.Address, to *common.Address, value *big.Int, data []byte, gasLimit uint64) ([]byte, uint64, error) {
	if block.GasLimit == 0 {
		return nil, 0, ErrInvalidBlockGasLimit
	}
	if gasLimit == 0 || gasLimit > block.GasLimit {
		gasLimit = block.GasLimit
	}
	value256 := new(uint256.Int)
	if value != nil {
		if value.Sign() < 0 {
			return nil, 0, errors.New("evm: call value cannot be negative")
		}
		var overflow bool
		value256, overflow = uint256.FromBig(value)
		if overflow {
			return nil, 0, errors.New("evm: call value exceeds 256 bits")
		}
	}

	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return nil, 0, errors.New("evm: engine is closed")
	}
	working := e.stateDB.Copy()
	config := e.config
	e.mu.RUnlock()

	number := new(big.Int).SetUint64(block.Number)
	rules := config.Rules(number, true, block.Timestamp)
	baseFee := new(big.Int)
	if block.BaseFee != nil {
		baseFee.Set(block.BaseFee)
	}
	blobBaseFee := new(big.Int)
	if block.BlobBaseFee != nil {
		blobBaseFee.Set(block.BlobBaseFee)
	}
	difficulty := new(big.Int)
	if block.Difficulty != nil {
		difficulty.Set(block.Difficulty)
	}
	random := common.Hash{}
	if block.Random != nil {
		random = *block.Random
	}
	getHash := block.GetHash
	if getHash == nil {
		getHash = func(uint64) common.Hash { return common.Hash{} }
	}
	message := &gethcore.Message{
		From:                  from,
		To:                    to,
		Nonce:                 working.GetNonce(from),
		Value:                 value256,
		GasLimit:              gasLimit,
		GasPrice:              new(uint256.Int),
		GasFeeCap:             new(uint256.Int),
		GasTipCap:             new(uint256.Int),
		Data:                  bytes.Clone(data),
		SkipNonceChecks:       true,
		SkipTransactionChecks: true,
	}
	working.Prepare(rules, from, block.Coinbase, to, vm.ActivePrecompiles(rules), nil)
	evmBlock := vm.BlockContext{
		CanTransfer: gethcore.CanTransfer,
		Transfer:    gethcore.Transfer,
		GetHash:     getHash,
		Coinbase:    block.Coinbase,
		GasLimit:    block.GasLimit,
		BlockNumber: number,
		Time:        block.Timestamp,
		Difficulty:  difficulty,
		BaseFee:     baseFee,
		BlobBaseFee: blobBaseFee,
		Random:      &random,
	}
	execution := vm.NewEVM(evmBlock, working, config, vm.Config{NoBaseFee: true})
	execution.SetTxContext(gethcore.NewEVMTxContext(message))
	result, err := gethcore.ApplyMessage(execution, message, nil)
	if err != nil {
		return nil, 0, err
	}
	if result.Failed() {
		return nil, result.UsedGas, result.Err
	}
	return bytes.Clone(result.ReturnData), result.UsedGas, nil
}

// IntrinsicGas returns the protocol-required minimum gas for a transaction
// envelope at the supplied block context.
func (e *Engine) IntrinsicGas(block BlockContext, from common.Address, to *common.Address, value *big.Int, data []byte, accessList types.AccessList) (uint64, error) {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return 0, errors.New("evm: engine is closed")
	}
	config := e.config
	e.mu.RUnlock()
	value256 := new(uint256.Int)
	if value != nil {
		var overflow bool
		value256, overflow = uint256.FromBig(value)
		if value.Sign() < 0 || overflow {
			return 0, errors.New("evm: transaction value is outside the uint256 range")
		}
	}
	rules := config.Rules(new(big.Int).SetUint64(block.Number), true, block.Timestamp)
	return gethcore.IntrinsicGas(data, accessList, nil, from, to, value256, rules)
}

// EstimateGas finds the minimum gas limit that allows a call to complete. It
// uses the same EVM state and fork rules as Call and never commits the copy.
func (e *Engine) EstimateGas(block BlockContext, from common.Address, to *common.Address, value *big.Int, data []byte, maxGas uint64) (uint64, error) {
	if maxGas == 0 || maxGas > block.GasLimit {
		maxGas = block.GasLimit
	}
	intrinsic, err := e.IntrinsicGas(block, from, to, value, data, nil)
	if err != nil {
		return 0, err
	}
	if intrinsic > maxGas {
		return 0, vm.ErrOutOfGas
	}
	if _, _, err := e.Call(block, from, to, value, data, maxGas); err != nil {
		return 0, err
	}
	low, high := intrinsic-1, maxGas
	for low+1 < high {
		middle := low + (high-low)/2
		if _, _, err := e.Call(block, from, to, value, data, middle); err == nil {
			high = middle
		} else if errors.Is(err, vm.ErrOutOfGas) {
			low = middle
		} else {
			return 0, err
		}
	}
	return high, nil
}

func (e *Engine) StateRoot() common.Hash {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.root
}

func (e *Engine) Height() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.height
}

func (e *Engine) Balance(addr common.Address) *big.Int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stateDB.GetBalance(addr).ToBig()
}

func (e *Engine) Nonce(addr common.Address) uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stateDB.GetNonce(addr)
}

func (e *Engine) Code(addr common.Address) []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return bytes.Clone(e.stateDB.GetCode(addr))
}

func (e *Engine) StorageAt(addr common.Address, key common.Hash) common.Hash {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stateDB.GetState(addr, key)
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	trieErr := e.trieDB.Close()
	var dbErr error
	if e.closeKV != nil {
		dbErr = e.closeKV()
	}
	return errors.Join(trieErr, dbErr)
}
