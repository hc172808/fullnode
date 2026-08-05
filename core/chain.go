package core

import (
        "errors"
        "math/big"
        "strings"
        "sync"

        "github.com/gydschain/fullnode/storage"
)

var (
        ErrBlockNotFound  = errors.New("block not found")
        ErrInvalidBlock   = errors.New("invalid block")
        ErrParentNotFound = errors.New("parent block not found")
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
        return c
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

        c.addBlock(b)

        // Apply transactions first so account state is updated before persisting.
        for _, tx := range b.Transactions {
                c.applyTx(tx)
        }

        // Persist block + updated account state atomically to LevelDB.
        c.persistBlock(b)

        return nil
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
        c.accountsMu.RLock()
        defer c.accountsMu.RUnlock()
        if a, ok := c.accounts[strings.ToLower(addr)]; ok {
                return new(big.Int).Set(a.Balance)
        }
        return big.NewInt(0)
}

// GetNonce returns the transaction count (nonce) for an address.
func (c *Chain) GetNonce(addr string) uint64 {
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
        defer c.txMu.RUnlock()
        tx, ok := c.txIndex[hash]
        return tx, ok
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
