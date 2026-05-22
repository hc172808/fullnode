package storage

import (
	"errors"
	"sync"
)

var ErrNotFound = errors.New("key not found")

type Storage interface {
	Get(key []byte) ([]byte, error)
	Put(key, value []byte) error
	Delete(key []byte) error
	Has(key []byte) (bool, error)
	NewBatch() Batch
	Iterator(prefix []byte) Iterator
	Close() error
}

type Batch interface {
	Put(key, value []byte)
	Delete(key []byte)
	Write() error
	Reset()
}

type Iterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Error() error
	Release()
}

// MemStorage is a fully in-memory Storage implementation (default, no deps).
type MemStorage struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemStorage() *MemStorage {
	return &MemStorage{data: make(map[string][]byte)}
}

func (m *MemStorage) Get(key []byte) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (m *MemStorage) Put(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[string(key)] = cp
	return nil
}

func (m *MemStorage) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, string(key))
	return nil
}

func (m *MemStorage) Has(key []byte) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[string(key)]
	return ok, nil
}

func (m *MemStorage) NewBatch() Batch {
	return &memBatch{store: m, ops: nil}
}

func (m *MemStorage) Iterator(prefix []byte) Iterator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pfx := string(prefix)
	pairs := make([]kv, 0)
	for k, v := range m.data {
		if len(pfx) == 0 || len(k) >= len(pfx) && k[:len(pfx)] == pfx {
			cv := make([]byte, len(v))
			copy(cv, v)
			pairs = append(pairs, kv{[]byte(k), cv})
		}
	}
	return &memIter{pairs: pairs, idx: -1}
}

func (m *MemStorage) Close() error { return nil }

type kv struct{ k, v []byte }

type memBatch struct {
	store *MemStorage
	ops   []batchOp
}

type batchOp struct {
	del bool
	k, v []byte
}

func (b *memBatch) Put(key, value []byte) {
	ck := make([]byte, len(key))
	copy(ck, key)
	cv := make([]byte, len(value))
	copy(cv, value)
	b.ops = append(b.ops, batchOp{k: ck, v: cv})
}

func (b *memBatch) Delete(key []byte) {
	ck := make([]byte, len(key))
	copy(ck, key)
	b.ops = append(b.ops, batchOp{del: true, k: ck})
}

func (b *memBatch) Write() error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	for _, op := range b.ops {
		if op.del {
			delete(b.store.data, string(op.k))
		} else {
			b.store.data[string(op.k)] = op.v
		}
	}
	return nil
}

func (b *memBatch) Reset() { b.ops = nil }

type memIter struct {
	pairs []kv
	idx   int
}

func (it *memIter) Next() bool {
	it.idx++
	return it.idx < len(it.pairs)
}

func (it *memIter) Key() []byte {
	if it.idx < 0 || it.idx >= len(it.pairs) {
		return nil
	}
	return it.pairs[it.idx].k
}

func (it *memIter) Value() []byte {
	if it.idx < 0 || it.idx >= len(it.pairs) {
		return nil
	}
	return it.pairs[it.idx].v
}

func (it *memIter) Error() error { return nil }
func (it *memIter) Release()     {}
