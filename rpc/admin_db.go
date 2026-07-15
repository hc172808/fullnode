package rpc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// AdminDB wraps a bbolt database for admin-only persistent storage.
// Each bucket is a "table"; values are JSON-encoded records.
type AdminDB struct {
	db *bolt.DB
}

// DBRecord is stored in every bucket.
type DBRecord struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

var builtinTables = []string{"notes", "settings", "addresses"}

// NewAdminDB opens (or creates) the admin database at dataDir/admin/admin.db.
func NewAdminDB(dataDir string) (*AdminDB, error) {
	dir := filepath.Join(dataDir, "admin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("admindb: mkdir: %w", err)
	}
	db, err := bolt.Open(filepath.Join(dir, "admin.db"), 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("admindb: open: %w", err)
	}
	adb := &AdminDB{db: db}
	if err := adb.ensureBuiltins(); err != nil {
		return nil, err
	}
	return adb, nil
}

func (a *AdminDB) ensureBuiltins() error {
	return a.db.Update(func(tx *bolt.Tx) error {
		for _, name := range builtinTables {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *AdminDB) Close() error { return a.db.Close() }

// Tables returns all bucket names.
func (a *AdminDB) Tables() ([]string, error) {
	var names []string
	err := a.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			names = append(names, string(name))
			return nil
		})
	})
	return names, err
}

// CreateTable creates a new bucket; errors if it already exists.
func (a *AdminDB) CreateTable(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("table name must be 1-64 characters")
	}
	return a.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(name)) != nil {
			return fmt.Errorf("table %q already exists", name)
		}
		_, err := tx.CreateBucket([]byte(name))
		return err
	})
}

// DropTable deletes a bucket; refuses to drop built-in tables.
func (a *AdminDB) DropTable(name string) error {
	for _, b := range builtinTables {
		if b == name {
			return fmt.Errorf("cannot drop built-in table %q", name)
		}
	}
	return a.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(name)) == nil {
			return fmt.Errorf("table %q not found", name)
		}
		return tx.DeleteBucket([]byte(name))
	})
}

// Records returns all records in a table.
func (a *AdminDB) Records(table string) ([]DBRecord, error) {
	var recs []DBRecord
	err := a.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(table))
		if b == nil {
			return fmt.Errorf("table %q not found", table)
		}
		return b.ForEach(func(k, v []byte) error {
			var rec DBRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				rec = DBRecord{Key: string(k), Value: v}
			}
			recs = append(recs, rec)
			return nil
		})
	})
	return recs, err
}

// PutRecord inserts or replaces a record. If key is empty a unique key is generated.
func (a *AdminDB) PutRecord(table, key string, value json.RawMessage) (string, error) {
	var finalKey string
	err := a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(table))
		if b == nil {
			return fmt.Errorf("table %q not found", table)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		var rec DBRecord
		if key == "" {
			id, err := b.NextSequence()
			if err != nil {
				return err
			}
			key = fmt.Sprintf("%d", id)
			rec = DBRecord{Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
		} else {
			// Preserve created_at if updating
			if existing := b.Get([]byte(key)); existing != nil {
				var old DBRecord
				if err := json.Unmarshal(existing, &old); err == nil {
					rec = DBRecord{Key: key, Value: value, CreatedAt: old.CreatedAt, UpdatedAt: now}
				} else {
					rec = DBRecord{Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
				}
			} else {
				rec = DBRecord{Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
			}
		}
		finalKey = key
		encoded, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), encoded)
	})
	return finalKey, err
}

// DeleteRecord removes a record by key.
func (a *AdminDB) DeleteRecord(table, key string) error {
	return a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(table))
		if b == nil {
			return fmt.Errorf("table %q not found", table)
		}
		if b.Get([]byte(key)) == nil {
			return fmt.Errorf("record %q not found", key)
		}
		return b.Delete([]byte(key))
	})
}

// TableCount returns the number of records in a table.
func (a *AdminDB) TableCount(table string) int {
	var n int
	_ = a.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(table))
		if b != nil {
			n = b.Stats().KeyN
		}
		return nil
	})
	return n
}
