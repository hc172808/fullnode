package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/gydschain/fullnode/evm"
	"github.com/gydschain/fullnode/storage"
)

var (
	ErrBlockNotFound  = errors.New("block not found")
	ErrInvalidBlock   = errors.New("invalid block")
	ErrParentNotFound = errors.New("parent block not found")
	ErrEVMUnavailable = errors.New("EVM transaction execution is not active on this chain")
)

// AccountState tracks the wei balance, nonce, and any genesis token balances.
type AccountState struct {
	Balance *big.Int
	Nonce   uint64
	Tokens  map[string]*big.Int // symbol → wei-scaled balance
}

type Chain struct {
	mu       sync.RWMutex
	blocks   []*Block
	byHash   map[string]*Block
	byNumber map[uint64]*Block
	genesis  *GenesisConfig
	dataDir  string
	db       storage.Storage

	accountsMu sync.RWMutex
	accounts   map[string]*AccountState

	txMu    sync.RWMutex
	txIndex map[string]*Transaction

	evmMu     sync.RWMutex
	evmEngine *evm.Engine
	evmErr    error

	pendingMu sync.RWMutex
	pending   map[string]*Transaction
}

func NewChain(genesis *GenesisConfig, dataDir string) *Chain {
	c := &Chain{
		blocks:   make([]*Block, 0, 1024),
		byHash:   make(map[string]*Block),
		byNumber: make(map[uint64]*Block),
		genesis:  genesis,
		dataDir:  dataDir,
		accounts: make(map[string]*AccountState),
		txIndex:  make(map[string]*Transaction),
		pending:  make(map[string]*Transaction),
	}
	for _, alloc := range genesis.Alloc {
		addr := strings.ToLower(alloc.Address)
		bal := alloc.Balance
		if bal == nil {
			bal = big.NewInt(0)
		}
		c.accounts[addr] = &AccountState{
			Balance: new(big.Int).Set(bal),
			Nonce:   alloc.Nonce,
			Tokens:  make(map[string]*big.Int),
		}
	}
	// Distribute genesis token balances (e.g. GYD stablecoin).
	for _, tok := range genesis.Tokens {
		for _, ta := range tok.Alloc {
			addr := strings.ToLower(ta.Address)
			if c.accounts[addr] == nil {
				c.accounts[addr] = &AccountState{
					Balance: big.NewInt(0),
					Tokens:  make(map[string]*big.Int),
				}
			}
			if c.accounts[addr].Tokens == nil {
				c.accounts[addr].Tokens = make(map[string]*big.Int)
			}
			amt := ta.Amount
			if amt == nil {
				amt = big.NewInt(0)
			}
			c.accounts[addr].Tokens[tok.Symbol] = new(big.Int).Set(amt)
		}
	}
	genBlock := GenesisBlock(genesis)
	c.addBlock(genBlock)

	if dataDir != "" {
		if err := c.openDB(); err != nil {
			// Non-fatal: log and continue with memory-only storage
			c.dataDir = ""
		} else if err := c.loadFromDB(); err != nil {
			// Non-fatal: continue from genesis
			c.Close()
			c.dataDir = ""
		}
	}
	if genesis.EVMActivationBlock > 0 {
		c.initializeEVM()
	}
	return c
}

// GenesisConfig returns the immutable genesis profile this chain was created
// with. Callers must not mutate the returned configuration.
func (c *Chain) GenesisConfig() *GenesisConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.genesis
}

func (c *Chain) initializeEVM() {
	activation := c.genesis.EVMActivationBlock
	head := c.blocks[len(c.blocks)-1].Header.Number
	if head+1 < activation {
		return
	}

	alloc := make(map[common.Address]evm.GenesisAccount, len(c.accounts))
	for address, account := range c.accounts {
		if !common.IsHexAddress(address) || account == nil {
			continue
		}
		alloc[common.HexToAddress(address)] = evm.GenesisAccount{
			Balance: new(big.Int).Set(account.Balance),
			Nonce:   account.Nonce,
		}
	}

	chainID := big.NewInt(c.genesis.ChainID)
	var engine *evm.Engine
	var err error
	if head >= activation {
		if c.dataDir == "" {
			err = errors.New("EVM state must be persistent after activation")
		} else {
			engine, err = evm.OpenExisting(c.dataDir, chainID)
		}
	} else if c.dataDir == "" {
		engine, err = evm.NewMemoryAt(chainID, head, alloc)
	} else {
		engine, err = evm.OpenAt(c.dataDir, chainID, head, alloc)
	}
	if err == nil && engine.Height() != head {
		err = fmt.Errorf("EVM state height %d does not match chain height %d", engine.Height(), head)
	}
	if err != nil {
		if engine != nil {
			_ = engine.Close()
		}
		c.evmMu.Lock()
		c.evmErr = fmt.Errorf("initialize EVM state at activation block %d: %w", activation, err)
		c.evmMu.Unlock()
		return
	}
	c.evmMu.Lock()
	c.evmEngine = engine
	c.evmMu.Unlock()
}

func (c *Chain) evmState() (*evm.Engine, error) {
	c.evmMu.RLock()
	defer c.evmMu.RUnlock()
	if c.evmErr != nil {
		return nil, c.evmErr
	}
	if c.evmEngine == nil {
		return nil, ErrEVMUnavailable
	}
	return c.evmEngine, nil
}

// EVMExecutionAvailable reports whether this chain has initialized its EVM
// state. Production networks remain disabled until their genesis profile
// defines a coordinated activation block.
func (c *Chain) EVMExecutionAvailable() (bool, error) {
	_, err := c.evmState()
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *Chain) EVMCode(address string) ([]byte, error) {
	engine, err := c.evmState()
	if err != nil {
		return nil, err
	}
	if !common.IsHexAddress(address) {
		return nil, errors.New("invalid EVM address")
	}
	return engine.Code(common.HexToAddress(address)), nil
}

func (c *Chain) EVMStorageAt(address string, key common.Hash) (common.Hash, error) {
	engine, err := c.evmState()
	if err != nil {
		return common.Hash{}, err
	}
	if !common.IsHexAddress(address) {
		return common.Hash{}, errors.New("invalid EVM address")
	}
	return engine.StorageAt(common.HexToAddress(address), key), nil
}

func (c *Chain) EVMCall(from string, to *string, value *big.Int, data []byte, gas uint64) ([]byte, uint64, error) {
	engine, err := c.evmState()
	if err != nil {
		return nil, 0, err
	}
	if from != "" && !common.IsHexAddress(from) {
		return nil, 0, errors.New("invalid call sender address")
	}
	fromAddress := common.Address{}
	if from != "" {
		fromAddress = common.HexToAddress(from)
	}
	var toAddress *common.Address
	if to != nil {
		if !common.IsHexAddress(*to) {
			return nil, 0, errors.New("invalid call recipient address")
		}
		address := common.HexToAddress(*to)
		toAddress = &address
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	block, getHash := c.latestEVMContextLocked()
	block.GetHash = getHash
	return engine.Call(block, fromAddress, toAddress, value, data, gas)
}

func (c *Chain) EVMEstimateGas(from string, to *string, value *big.Int, data []byte, gas uint64) (uint64, error) {
	engine, err := c.evmState()
	if err != nil {
		return 0, err
	}
	if from != "" && !common.IsHexAddress(from) {
		return 0, errors.New("invalid call sender address")
	}
	fromAddress := common.Address{}
	if from != "" {
		fromAddress = common.HexToAddress(from)
	}
	var toAddress *common.Address
	if to != nil {
		if !common.IsHexAddress(*to) {
			return 0, errors.New("invalid call recipient address")
		}
		address := common.HexToAddress(*to)
		toAddress = &address
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	block, getHash := c.latestEVMContextLocked()
	block.GetHash = getHash
	return engine.EstimateGas(block, fromAddress, toAddress, value, data, gas)
}

// latestEVMContextLocked builds a stable latest-block context. The caller must
// hold c.mu.RLock or c.mu.Lock.
func (c *Chain) latestEVMContextLocked() (evm.BlockContext, func(uint64) common.Hash) {
	head := c.blocks[len(c.blocks)-1]
	hashes := make(map[uint64]common.Hash, 257)
	start := uint64(0)
	if head.Header.Number > 256 {
		start = head.Header.Number - 256
	}
	for number := start; number <= head.Header.Number; number++ {
		if ancestor := c.byNumber[number]; ancestor != nil {
			hashes[number] = common.HexToHash(ancestor.Hash)
		}
	}
	getHash := func(number uint64) common.Hash { return hashes[number] }
	random := common.Hash{}
	return evm.BlockContext{
		Number:      head.Header.Number,
		Timestamp:   uint64(max(head.Header.Timestamp, 0)),
		GasLimit:    head.Header.GasLimit,
		Coinbase:    common.HexToAddress(head.Header.Validator),
		BaseFee:     big.NewInt(1_000_000_000),
		BlobBaseFee: big.NewInt(1),
		Difficulty:  head.Header.Difficulty,
		Random:      &random,
	}, getHash
}

// SubmitRawTransaction validates an Ethereum-signed transaction and adds it to
// the local pending pool. It never mutates confirmed state; transactions become
// final only when an EVM-enabled block is produced.
func (c *Chain) SubmitRawTransaction(raw []byte) (string, error) {
	engine, err := c.evmState()
	if err != nil {
		return "", err
	}
	tx, sender, err := evm.DecodeSignedTransaction(raw, big.NewInt(c.genesis.ChainID))
	if err != nil {
		return "", err
	}
	if tx.Gas() == 0 || tx.Gas() > 30_000_000 {
		return "", errors.New("transaction gas limit is outside the block limit")
	}
	if tx.GasFeeCap().Cmp(big.NewInt(1_000_000_000)) < 0 {
		return "", errors.New("transaction fee cap is below the current base fee")
	}
	hash := tx.Hash().Hex()

	c.txMu.RLock()
	_, confirmed := c.txIndex[hash]
	c.txMu.RUnlock()
	if confirmed {
		return hash, nil
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if _, exists := c.pending[hash]; exists {
		return hash, nil
	}
	if len(c.pending) >= 4096 {
		return "", errors.New("pending transaction pool is full")
	}

	expectedNonce := engine.Nonce(sender)
	requiredBalance := new(big.Int)
	senderPending := make([]*Transaction, 0)
	for _, pending := range c.pending {
		if strings.EqualFold(pending.From, sender.Hex()) {
			senderPending = append(senderPending, pending)
		}
	}
	sort.Slice(senderPending, func(i, j int) bool {
		return senderPending[i].Nonce < senderPending[j].Nonce
	})
	for _, pending := range senderPending {
		if pending.Nonce < expectedNonce {
			continue
		}
		if pending.Nonce != expectedNonce {
			return "", errors.New("pending transaction sequence has a nonce gap")
		}
		expectedNonce++
		if pending.GasFeeCap != nil {
			cost := new(big.Int).Mul(pending.GasFeeCap, new(big.Int).SetUint64(pending.GasLimit))
			if pending.Value != nil {
				cost.Add(cost, pending.Value)
			}
			requiredBalance.Add(requiredBalance, cost)
		}
	}
	if tx.Nonce() != expectedNonce {
		return "", fmt.Errorf("invalid transaction nonce: got %d, want %d", tx.Nonce(), expectedNonce)
	}
	requiredBalance.Add(requiredBalance, tx.Cost())
	if requiredBalance.Cmp(engine.Balance(sender)) > 0 {
		return "", errors.New("sender balance cannot cover pending transaction costs")
	}

	c.pending[hash] = signedTransaction(tx, sender, raw)
	return hash, nil
}

func signedTransaction(tx *types.Transaction, sender common.Address, raw []byte) *Transaction {
	to := ""
	txType := TxTypeTransfer
	if tx.To() != nil {
		to = tx.To().Hex()
	}
	if tx.To() == nil || len(tx.Data()) > 0 {
		txType = TxTypeContract
	}
	v, r, s := tx.RawSignatureValues()
	data := append([]byte(nil), tx.Data()...)
	return &Transaction{
		Hash:           tx.Hash().Hex(),
		From:           sender.Hex(),
		To:             to,
		Value:          new(big.Int).Set(tx.Value()),
		GasLimit:       tx.Gas(),
		GasPrice:       new(big.Int).Set(tx.GasFeeCap()),
		GasUsed:        0,
		Nonce:          tx.Nonce(),
		Data:           data,
		RawTransaction: append([]byte(nil), raw...),
		ChainID:        new(big.Int).Set(tx.ChainId()),
		GasFeeCap:      new(big.Int).Set(tx.GasFeeCap()),
		GasTipCap:      new(big.Int).Set(tx.GasTipCap()),
		EVMType:        tx.Type(),
		V:              new(big.Int).Set(v),
		R:              new(big.Int).Set(r),
		S:              new(big.Int).Set(s),
		Type:           txType,
		Status:         "pending",
		Timestamp:      time.Now().Unix(),
	}
}

func (c *Chain) PendingTransactions(max int) []*Transaction {
	if max <= 0 {
		return nil
	}
	c.pendingMu.RLock()
	defer c.pendingMu.RUnlock()
	out := make([]*Transaction, 0, len(c.pending))
	for _, tx := range c.pending {
		out = append(out, tx)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return strings.ToLower(out[i].From) < strings.ToLower(out[j].From)
		}
		return out[i].Nonce < out[j].Nonce
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func (c *Chain) addBlock(b *Block) {
	c.blocks = append(c.blocks, b)
	c.byHash[b.Hash] = b
	c.byNumber[b.Header.Number] = b
}

func (c *Chain) Head() *Block {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.blocks) == 0 {
		return nil
	}
	return c.blocks[len(c.blocks)-1]
}

func (c *Chain) Height() uint64 {
	h := c.Head()
	if h == nil {
		return 0
	}
	return h.Header.Number
}

func (c *Chain) GetByHash(hash string) (*Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.byHash[hash]
	if !ok {
		return nil, ErrBlockNotFound
	}
	return b, nil
}

func (c *Chain) GetByNumber(num uint64) (*Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.byNumber[num]
	if !ok {
		return nil, ErrBlockNotFound
	}
	return b, nil
}

func (c *Chain) LatestBlocks(n int) []*Block {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n > len(c.blocks) {
		n = len(c.blocks)
	}
	start := len(c.blocks) - n
	result := make([]*Block, n)
	copy(result, c.blocks[start:])
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func (c *Chain) InsertBlock(b *Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if b == nil || b.Header == nil {
		return ErrInvalidBlock
	}
	if _, exists := c.byHash[b.Hash]; exists {
		return nil
	}

	head := c.blocks[len(c.blocks)-1]
	if b.Header.ParentHash != head.Hash {
		return ErrParentNotFound
	}
	if b.Header.Number != head.Header.Number+1 {
		return ErrInvalidBlock
	}

	evmActive := c.genesis.EVMActivationBlock > 0 && b.Header.Number >= c.genesis.EVMActivationBlock
	if evmActive {
		if err := c.insertEVMBlock(b); err != nil {
			return err
		}
		return nil
	}
	for _, tx := range b.Transactions {
		if tx != nil && len(tx.RawTransaction) > 0 {
			return ErrEVMUnavailable
		}
	}

	c.addBlock(b)

	// Apply transactions first so account state is updated before persisting.
	for _, tx := range b.Transactions {
		c.applyTx(tx)
	}

	// Persist block + updated account state atomically to LevelDB.
	if err := c.persistBlock(b); err != nil {
		return err
	}

	return nil
}

// insertEVMBlock is called with c.mu held. The disposable test node has no P2P
// peers, so its locally produced blocks are the only EVM blocks accepted here.
func (c *Chain) insertEVMBlock(b *Block) error {
	engine, err := c.evmState()
	if err != nil {
		return err
	}

	evmTxs := make([]*types.Transaction, 0, len(b.Transactions))
	transactions := make([]*Transaction, 0, len(b.Transactions))
	for index, tx := range b.Transactions {
		if tx == nil || len(tx.RawTransaction) == 0 {
			return fmt.Errorf("EVM block transaction %d has no signed raw payload", index)
		}
		decoded, sender, err := evm.DecodeSignedTransaction(tx.RawTransaction, big.NewInt(c.genesis.ChainID))
		if err != nil {
			return fmt.Errorf("decode EVM block transaction %d: %w", index, err)
		}
		evmTxs = append(evmTxs, decoded)
		transactions = append(transactions, signedTransaction(decoded, sender, tx.RawTransaction))
	}

	random := common.Hash{}
	blockContext := evm.BlockContext{
		Number:      b.Header.Number,
		Timestamp:   uint64(max(b.Header.Timestamp, 0)),
		GasLimit:    b.Header.GasLimit,
		Coinbase:    common.HexToAddress(b.Header.Validator),
		BaseFee:     big.NewInt(1_000_000_000),
		BlobBaseFee: big.NewInt(1),
		Difficulty:  b.Header.Difficulty,
		Random:      &random,
		GetHash: func(number uint64) common.Hash {
			if ancestor := c.byNumber[number]; ancestor != nil {
				return common.HexToHash(ancestor.Hash)
			}
			return common.Hash{}
		},
	}
	result, err := engine.ApplyBlock(blockContext, evmTxs)
	if err != nil {
		return err
	}
	if len(result.Receipts) != len(transactions) {
		return errors.New("EVM engine returned an unexpected receipt count")
	}

	b.Transactions = transactions
	b.Header.StateRoot = result.StateRoot.Hex()
	b.Header.TxRoot = result.TransactionRoot.Hex()
	b.Header.ReceiptRoot = result.ReceiptRoot.Hex()
	b.Header.GasUsed = result.GasUsed
	b.Header.Size = estimateBlockSize(transactions)

	var globalLogIndex uint
	for index, tx := range transactions {
		receipt := result.Receipts[index]
		tx.BlockNum = b.Header.Number
		tx.GasUsed = receipt.GasUsed
		if receipt.Status == types.ReceiptStatusSuccessful {
			tx.Status = "success"
		} else {
			tx.Status = "failed"
		}
		for _, entry := range receipt.Logs {
			entry.BlockNumber = b.Header.Number
			entry.TxHash = receipt.TxHash
			entry.TxIndex = uint(index)
			entry.Index = globalLogIndex
			globalLogIndex++
		}
	}

	b.Header.Hash = b.Header.ComputeHash()
	b.Hash = b.Header.Hash
	for index, tx := range transactions {
		receipt := result.Receipts[index]
		receipt.BlockHash = common.HexToHash(b.Hash)
		for _, entry := range receipt.Logs {
			entry.BlockHash = common.HexToHash(b.Hash)
		}
		rawReceipt, err := json.Marshal(receipt)
		if err != nil {
			return fmt.Errorf("encode EVM receipt for transaction %s: %w", tx.Hash, err)
		}
		tx.Receipt = rawReceipt
	}

	c.syncEVMAccounts(transactions, result.Receipts)
	if err := c.persistBlock(b); err != nil {
		c.evmMu.Lock()
		c.evmErr = fmt.Errorf("persisted EVM block %d could not be committed: %w", b.Header.Number, err)
		c.evmMu.Unlock()
		return err
	}
	c.addBlock(b)
	c.txMu.Lock()
	for _, tx := range transactions {
		c.txIndex[tx.Hash] = tx
	}
	c.txMu.Unlock()
	c.pendingMu.Lock()
	for _, tx := range transactions {
		delete(c.pending, tx.Hash)
	}
	c.pendingMu.Unlock()
	return nil
}

func (c *Chain) syncEVMAccounts(transactions []*Transaction, receipts types.Receipts) {
	engine, err := c.evmState()
	if err != nil {
		return
	}
	addresses := make(map[string]common.Address)
	for _, tx := range transactions {
		if common.IsHexAddress(tx.From) {
			addresses[strings.ToLower(tx.From)] = common.HexToAddress(tx.From)
		}
		if common.IsHexAddress(tx.To) {
			addresses[strings.ToLower(tx.To)] = common.HexToAddress(tx.To)
		}
	}
	for _, receipt := range receipts {
		if receipt != nil && receipt.ContractAddress != (common.Address{}) {
			addresses[strings.ToLower(receipt.ContractAddress.Hex())] = receipt.ContractAddress
		}
	}
	c.accountsMu.Lock()
	defer c.accountsMu.Unlock()
	for key, address := range addresses {
		account := c.accounts[key]
		if account == nil {
			account = &AccountState{Balance: new(big.Int), Tokens: make(map[string]*big.Int)}
			c.accounts[key] = account
		}
		account.Balance = engine.Balance(address)
		account.Nonce = engine.Nonce(address)
	}
}

// applyTx updates account state from a confirmed transaction.
// Safe to call while holding c.mu since it uses its own accountsMu/txMu.
func (c *Chain) applyTx(tx *Transaction) {
	c.txMu.Lock()
	c.txIndex[tx.Hash] = tx
	c.txMu.Unlock()

	if tx.Value == nil || tx.Value.Sign() == 0 {
		return
	}

	c.accountsMu.Lock()
	defer c.accountsMu.Unlock()

	from := strings.ToLower(tx.From)
	to := strings.ToLower(tx.To)

	if _, ok := c.accounts[from]; !ok {
		c.accounts[from] = &AccountState{Balance: new(big.Int)}
	}
	if to != "" {
		if _, ok := c.accounts[to]; !ok {
			c.accounts[to] = &AccountState{Balance: new(big.Int)}
		}
	}

	// Compute total cost = value + gas
	cost := new(big.Int).Set(tx.Value)
	if tx.GasPrice != nil && tx.GasUsed > 0 {
		gasCost := new(big.Int).Mul(tx.GasPrice, big.NewInt(int64(tx.GasUsed)))
		cost.Add(cost, gasCost)
	}

	// Only apply if sender can afford it
	if c.accounts[from].Balance.Cmp(cost) >= 0 {
		c.accounts[from].Balance.Sub(c.accounts[from].Balance, cost)
		c.accounts[from].Nonce++
		if to != "" {
			c.accounts[to].Balance.Add(c.accounts[to].Balance, tx.Value)
		}
	}
}

// GetTokenBalance returns the genesis-allocated token balance for an address.
func (c *Chain) GetTokenBalance(addr, symbol string) *big.Int {
	c.accountsMu.RLock()
	defer c.accountsMu.RUnlock()
	if a, ok := c.accounts[strings.ToLower(addr)]; ok {
		if a.Tokens != nil {
			if b, ok := a.Tokens[symbol]; ok {
				return new(big.Int).Set(b)
			}
		}
	}
	return big.NewInt(0)
}

// GetAllTokenBalances returns every token balance held by an address.
func (c *Chain) GetAllTokenBalances(addr string) map[string]*big.Int {
	c.accountsMu.RLock()
	defer c.accountsMu.RUnlock()
	out := make(map[string]*big.Int)
	if a, ok := c.accounts[strings.ToLower(addr)]; ok {
		for sym, bal := range a.Tokens {
			out[sym] = new(big.Int).Set(bal)
		}
	}
	return out
}

// TokenInfoList returns metadata for every token defined in the genesis config.
func (c *Chain) TokenInfoList() []TokenDefinition {
	return c.genesis.Tokens
}

// GetBalance returns the wei balance of an address.
func (c *Chain) GetBalance(addr string) *big.Int {
	if engine, err := c.evmState(); err == nil && common.IsHexAddress(addr) {
		return engine.Balance(common.HexToAddress(addr))
	}
	c.accountsMu.RLock()
	defer c.accountsMu.RUnlock()
	if a, ok := c.accounts[strings.ToLower(addr)]; ok {
		return new(big.Int).Set(a.Balance)
	}
	return big.NewInt(0)
}

// GetNonce returns the transaction count (nonce) for an address.
func (c *Chain) GetNonce(addr string) uint64 {
	if engine, err := c.evmState(); err == nil && common.IsHexAddress(addr) {
		return engine.Nonce(common.HexToAddress(addr))
	}
	c.accountsMu.RLock()
	defer c.accountsMu.RUnlock()
	if a, ok := c.accounts[strings.ToLower(addr)]; ok {
		return a.Nonce
	}
	return 0
}

// GetTransaction returns a confirmed transaction by hash.
func (c *Chain) GetTransaction(hash string) (*Transaction, bool) {
	c.txMu.RLock()
	tx, ok := c.txIndex[hash]
	c.txMu.RUnlock()
	if ok {
		return tx, true
	}
	c.pendingMu.RLock()
	defer c.pendingMu.RUnlock()
	tx, ok = c.pending[hash]
	return tx, ok
}

func (c *Chain) PendingNonce(addr string) uint64 {
	nonce := c.GetNonce(addr)
	c.pendingMu.RLock()
	defer c.pendingMu.RUnlock()
	pending := make([]*Transaction, 0)
	for _, tx := range c.pending {
		if strings.EqualFold(tx.From, addr) {
			pending = append(pending, tx)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Nonce < pending[j].Nonce })
	for _, tx := range pending {
		if tx.Nonce == nonce {
			nonce++
		}
	}
	return nonce
}

// AddToTxIndex adds an external transaction (e.g. from sendRawTransaction) to the index.
func (c *Chain) AddToTxIndex(tx *Transaction) {
	c.txMu.Lock()
	defer c.txMu.Unlock()
	c.txIndex[tx.Hash] = tx
}

func (c *Chain) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	head := c.blocks[len(c.blocks)-1]
	var totalTxs int
	for _, b := range c.blocks {
		totalTxs += len(b.Transactions)
	}
	return map[string]interface{}{
		"blockHeight":        head.Header.Number,
		"headHash":           head.Hash,
		"chainId":            c.genesis.ChainID,
		"networkName":        c.genesis.NetworkName,
		"totalBlocks":        len(c.blocks),
		"totalTxs":           totalTxs,
		"lastBlockTimestamp": head.Header.Timestamp,
		"validator":          head.Header.Validator,
	}
}
