// Package mempool implements a priority transaction pool for the GYDS chain.
// Transactions are ordered by gas price (highest first) and within the same
// sender by nonce (lowest first).  The pool enforces a maximum size and
// evicts the lowest-priced transactions when full.
package mempool

import (
	"container/heap"
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/gydschain/litenode/core"
	"github.com/rs/zerolog/log"
)

const (
	DefaultMaxSize    = 4096
	DefaultTTL        = 10 * time.Minute
	DefaultMaxPerAddr = 64
)

var (
	ErrPoolFull      = errors.New("mempool: pool is full")
	ErrNonceTooLow   = errors.New("mempool: nonce too low")
	ErrAlreadyKnown  = errors.New("mempool: transaction already known")
	ErrUnderpriced   = errors.New("mempool: transaction underpriced")
	ErrTooManyQueued = errors.New("mempool: too many queued transactions for sender")
)

// Mempool is a thread-safe priority transaction pool.
type Mempool struct {
	mu      sync.RWMutex
	pending txHeap
	index   map[string]*entry // hash → entry
	bySender map[string][]*entry // sender → sorted by nonce

	maxSize    int
	maxPerAddr int
	ttl        time.Duration

	getNonce   func(addr string) uint64
	getBalance func(addr string) *big.Int
}

type entry struct {
	tx        *core.Transaction
	addedAt   time.Time
	heapIndex int
}

// New creates a Mempool.  getNonce and getBalance are callbacks into chain state.
func New(getNonce func(string) uint64, getBalance func(string) *big.Int) *Mempool {
	m := &Mempool{
		index:      make(map[string]*entry),
		bySender:   make(map[string][]*entry),
		maxSize:    DefaultMaxSize,
		maxPerAddr: DefaultMaxPerAddr,
		ttl:        DefaultTTL,
		getNonce:   getNonce,
		getBalance: getBalance,
	}
	heap.Init(&m.pending)
	go m.expireLoop()
	return m
}

// Add validates and inserts a transaction.  Returns the tx hash or an error.
func (m *Mempool) Add(tx *core.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.index[tx.Hash]; ok {
		return ErrAlreadyKnown
	}

	chainNonce := m.getNonce(tx.From)
	if tx.Nonce < chainNonce {
		return ErrNonceTooLow
	}

	senderTxs := m.bySender[tx.From]
	if len(senderTxs) >= m.maxPerAddr {
		return ErrTooManyQueued
	}

	// Evict cheapest if full
	if len(m.pending) >= m.maxSize {
		cheapest := m.pending[0]
		if cheapest.tx.GasPrice.Cmp(tx.GasPrice) >= 0 {
			return ErrPoolFull
		}
		m.evict(cheapest)
	}

	e := &entry{tx: tx, addedAt: time.Now()}
	heap.Push(&m.pending, e)
	m.index[tx.Hash] = e
	m.bySender[tx.From] = insertSorted(senderTxs, e)

	log.Debug().Str("hash", tx.Hash).Str("from", tx.From).
		Uint64("nonce", tx.Nonce).Msg("mempool: tx added")
	return nil
}

// Get returns a transaction by hash.
func (m *Mempool) Get(hash string) (*core.Transaction, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.index[hash]
	if !ok {
		return nil, false
	}
	return e.tx, true
}

// Remove deletes a transaction from the pool (call after it is included in a block).
func (m *Mempool) Remove(hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.index[hash]; ok {
		m.evict(e)
	}
}

// Pending returns up to n highest-priority transactions ready for inclusion
// (nonce == chainNonce for that sender).
func (m *Mempool) Pending(n int) []*core.Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*core.Transaction
	seen := make(map[string]bool)

	// Build a copy of the heap sorted by gas price to iterate without mutating
	sorted := make(txHeap, len(m.pending))
	copy(sorted, m.pending)

	for len(sorted) > 0 && len(result) < n {
		e := heap.Pop(&sorted).(*entry)
		if seen[e.tx.From] {
			continue
		}
		chainNonce := m.getNonce(e.tx.From)
		if e.tx.Nonce == chainNonce {
			result = append(result, e.tx)
			seen[e.tx.From] = true
		}
	}
	return result
}

// Size returns the number of transactions in the pool.
func (m *Mempool) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pending)
}

// Stats returns summary statistics.
func (m *Mempool) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return map[string]interface{}{
		"size":    len(m.pending),
		"senders": len(m.bySender),
	}
}

// evict removes an entry (caller must hold write lock).
func (m *Mempool) evict(e *entry) {
	heap.Remove(&m.pending, e.heapIndex)
	delete(m.index, e.tx.Hash)
	senderTxs := m.bySender[e.tx.From]
	updated := make([]*entry, 0, len(senderTxs))
	for _, se := range senderTxs {
		if se.tx.Hash != e.tx.Hash {
			updated = append(updated, se)
		}
	}
	if len(updated) == 0 {
		delete(m.bySender, e.tx.From)
	} else {
		m.bySender[e.tx.From] = updated
	}
}

// expireLoop removes transactions that have been in the pool too long.
func (m *Mempool) expireLoop() {
	ticker := time.NewTicker(m.ttl / 4)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		m.mu.Lock()
		for _, e := range m.index {
			if now.Sub(e.addedAt) > m.ttl {
				m.evict(e)
			}
		}
		m.mu.Unlock()
	}
}

// insertSorted inserts e into entries sorted ascending by nonce.
func insertSorted(entries []*entry, e *entry) []*entry {
	for i, se := range entries {
		if e.tx.Nonce < se.tx.Nonce {
			result := make([]*entry, len(entries)+1)
			copy(result, entries[:i])
			result[i] = e
			copy(result[i+1:], entries[i:])
			return result
		}
	}
	return append(entries, e)
}

// ── heap.Interface ──────────────────────────────────────────────────────────
// Max-heap by GasPrice (highest first).

type txHeap []*entry

func (h txHeap) Len() int { return len(h) }
func (h txHeap) Less(i, j int) bool {
	return h[i].tx.GasPrice.Cmp(h[j].tx.GasPrice) > 0
}
func (h txHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}
func (h *txHeap) Push(x interface{}) {
	e := x.(*entry)
	e.heapIndex = len(*h)
	*h = append(*h, e)
}
func (h *txHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	e.heapIndex = -1
	return e
}
