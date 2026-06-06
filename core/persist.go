package core

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/gydschain/fullnode/storage"
)

// Key-space layout inside LevelDB:
//
//	blk:<8-byte-BE-blockNum>  →  JSON-encoded Block
//	acc:<lowercased-address>  →  JSON-encoded accountStore
//
// Using big-endian block numbers means lexicographic order == numeric order,
// so a prefix scan over "blk:" returns blocks in ascending height.

var (
	blkPrefix = []byte("blk:")
	accPrefix = []byte("acc:")
)

// accountStore is the on-disk representation of AccountState.
// Balance is stored as a decimal string so big.Int precision is preserved.
type accountStore struct {
	Balance string `json:"balance"`
	Nonce   uint64 `json:"nonce"`
}

// blkKey encodes a block number as a fixed 12-byte key (prefix + 8-byte BE).
func blkKey(num uint64) []byte {
	key := make([]byte, 4+8)
	copy(key, blkPrefix)
	binary.BigEndian.PutUint64(key[4:], num)
	return key
}

// accKey returns the LevelDB key for an account address.
func accKey(addr string) []byte {
	return append(append([]byte{}, accPrefix...), []byte(addr)...)
}

// openDB opens (or creates) the LevelDB state database.
func (c *Chain) openDB() error {
	if c.dataDir == "" {
		return nil
	}
	dbPath := filepath.Join(c.dataDir, "state.db")
	if err := os.MkdirAll(c.dataDir, 0o755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}
	db, err := storage.NewLevelDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening LevelDB at %s: %w", dbPath, err)
	}
	c.db = db
	log.Info().Str("path", dbPath).Msg("LevelDB state database opened")
	return nil
}

// Close flushes and closes the LevelDB handle. Safe to call multiple times.
func (c *Chain) Close() {
	if c.db != nil {
		if err := c.db.Close(); err != nil {
			log.Error().Err(err).Msg("Error closing LevelDB")
		}
		c.db = nil
		log.Info().Msg("LevelDB closed")
	}
}

// persistBlock writes a sealed block and the resulting account state for every
// address touched by its transactions into LevelDB as an atomic batch.
// Must be called AFTER applyTx so account state is already updated.
func (c *Chain) persistBlock(b *Block) {
	if c.db == nil {
		return
	}

	batch := c.db.NewBatch()

	// ── Block ────────────────────────────────────────────────────────────────
	blockData, err := json.Marshal(b)
	if err != nil {
		log.Error().Err(err).Uint64("block", b.Header.Number).Msg("Failed to marshal block")
		return
	}
	batch.Put(blkKey(b.Header.Number), blockData)

	// ── Account state for all addresses touched by this block ─────────────
	for _, addr := range touchedAddresses(b) {
		c.accountsMu.RLock()
		state, ok := c.accounts[addr]
		c.accountsMu.RUnlock()
		if !ok {
			continue
		}
		as := accountStore{
			Balance: state.Balance.String(),
			Nonce:   state.Nonce,
		}
		encoded, err := json.Marshal(as)
		if err != nil {
			continue
		}
		batch.Put(accKey(addr), encoded)
	}

	if err := batch.Write(); err != nil {
		log.Error().Err(err).Uint64("block", b.Header.Number).Msg("Failed to commit block to LevelDB")
	}
}

// loadFromDB reconstructs chain state from LevelDB on startup.
// Blocks are loaded in ascending order; account balances are loaded directly
// from their dedicated keys (no transaction replay needed).
func (c *Chain) loadFromDB() error {
	if c.db == nil {
		return nil
	}

	// ── Blocks ───────────────────────────────────────────────────────────────
	blkIter := c.db.Iterator(blkPrefix)
	defer blkIter.Release()

	loaded := 0
	for blkIter.Next() {
		var b Block
		if err := json.Unmarshal(blkIter.Value(), &b); err != nil {
			log.Warn().Err(err).Msg("Skipping malformed block in LevelDB")
			continue
		}
		if b.Header.Number == 0 {
			continue // Genesis is always re-derived from config, never persisted
		}
		c.addBlock(&b)
		// Index transactions (no balance replay — loaded separately below)
		c.txMu.Lock()
		for _, tx := range b.Transactions {
			c.txIndex[tx.Hash] = tx
		}
		c.txMu.Unlock()
		loaded++
	}
	if err := blkIter.Error(); err != nil {
		return fmt.Errorf("iterating blocks in LevelDB: %w", err)
	}

	// ── Account state ─────────────────────────────────────────────────────────
	// Overwrite genesis-seeded balances with the real persisted values.
	accIter := c.db.Iterator(accPrefix)
	defer accIter.Release()

	loadedAccts := 0
	for accIter.Next() {
		addr := string(accIter.Key()[len(accPrefix):])
		var as accountStore
		if err := json.Unmarshal(accIter.Value(), &as); err != nil {
			log.Warn().Err(err).Str("addr", addr).Msg("Skipping malformed account in LevelDB")
			continue
		}
		bal, ok := new(big.Int).SetString(as.Balance, 10)
		if !ok {
			bal = big.NewInt(0)
		}
		c.accountsMu.Lock()
		c.accounts[addr] = &AccountState{Balance: bal, Nonce: as.Nonce}
		c.accountsMu.Unlock()
		loadedAccts++
	}
	if err := accIter.Error(); err != nil {
		return fmt.Errorf("iterating accounts in LevelDB: %w", err)
	}

	if loaded > 0 || loadedAccts > 0 {
		log.Info().
			Int("blocks", loaded).
			Int("accounts", loadedAccts).
			Uint64("height", c.Height()).
			Msg("Chain state restored from LevelDB")
	}
	return nil
}

// touchedAddresses returns the de-duplicated set of lowercase addresses
// involved in a block's transactions (senders and recipients).
func touchedAddresses(b *Block) []string {
	seen := make(map[string]struct{}, len(b.Transactions)*2)
	for _, tx := range b.Transactions {
		if tx.From != "" {
			seen[strings.ToLower(tx.From)] = struct{}{}
		}
		if tx.To != "" {
			seen[strings.ToLower(tx.To)] = struct{}{}
		}
	}
	addrs := make([]string, 0, len(seen))
	for addr := range seen {
		addrs = append(addrs, addr)
	}
	return addrs
}
