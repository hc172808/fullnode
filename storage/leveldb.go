package storage

import (
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// LevelDB implements the Storage interface backed by LevelDB on disk.
type LevelDB struct {
	db *leveldb.DB
}

// NewLevelDB opens (or creates) a LevelDB database at the given path.
func NewLevelDB(path string) (*LevelDB, error) {
	o := &opt.Options{
		WriteBuffer:            64 * opt.MiB,
		BlockCacheCapacity:     16 * opt.MiB,
		CompactionTableSize:    4 * opt.MiB,
		OpenFilesCacheCapacity: 64,
	}
	db, err := leveldb.OpenFile(path, o)
	if err != nil {
		return nil, err
	}
	return &LevelDB{db: db}, nil
}

func (l *LevelDB) Get(key []byte) ([]byte, error) {
	v, err := l.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, ErrNotFound
	}
	return v, err
}

func (l *LevelDB) Put(key, value []byte) error {
	return l.db.Put(key, value, nil)
}

func (l *LevelDB) Delete(key []byte) error {
	return l.db.Delete(key, nil)
}

func (l *LevelDB) Has(key []byte) (bool, error) {
	return l.db.Has(key, nil)
}

func (l *LevelDB) Close() error {
	return l.db.Close()
}

func (l *LevelDB) NewBatch() Batch {
	return &levelDBBatch{db: l.db, batch: new(leveldb.Batch)}
}

func (l *LevelDB) Iterator(prefix []byte) Iterator {
	var r *util.Range
	if len(prefix) > 0 {
		r = util.BytesPrefix(prefix)
	}
	return &levelDBIterator{iter: l.db.NewIterator(r, nil)}
}

// ── Batch ─────────────────────────────────────────────────────────────────────

type levelDBBatch struct {
	db    *leveldb.DB
	batch *leveldb.Batch
}

func (b *levelDBBatch) Put(key, value []byte) {
	b.batch.Put(key, value)
}

func (b *levelDBBatch) Delete(key []byte) {
	b.batch.Delete(key)
}

func (b *levelDBBatch) Write() error {
	return b.db.Write(b.batch, nil)
}

func (b *levelDBBatch) Reset() {
	b.batch.Reset()
}

// ── Iterator ──────────────────────────────────────────────────────────────────

type levelDBIterator struct {
	iter iterator.Iterator
}

func (it *levelDBIterator) Next() bool { return it.iter.Next() }

func (it *levelDBIterator) Key() []byte {
	k := it.iter.Key()
	cp := make([]byte, len(k))
	copy(cp, k)
	return cp
}

func (it *levelDBIterator) Value() []byte {
	v := it.iter.Value()
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp
}

func (it *levelDBIterator) Error() error { return it.iter.Error() }
func (it *levelDBIterator) Release()     { it.iter.Release() }
