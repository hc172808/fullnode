package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

const chaindataSubdir = "chaindata"

func (c *Chain) chaindataPath() string {
	return filepath.Join(c.dataDir, chaindataSubdir)
}

func (c *Chain) blockFilePath(number uint64) string {
	return filepath.Join(c.chaindataPath(), fmt.Sprintf("block_%012d.json", number))
}

func (c *Chain) initDataDir() error {
	if c.dataDir == "" {
		return nil
	}
	return os.MkdirAll(c.chaindataPath(), 0o755)
}

func (c *Chain) persistBlock(b *Block) {
	if c.dataDir == "" {
		return
	}
	data, err := json.Marshal(b)
	if err != nil {
		log.Error().Err(err).Uint64("block", b.Header.Number).Msg("Failed to marshal block for persistence")
		return
	}
	path := c.blockFilePath(b.Header.Number)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Error().Err(err).Str("path", path).Msg("Failed to write block to disk")
	}
}

func (c *Chain) loadFromDisk() error {
	if c.dataDir == "" {
		return nil
	}
	dir := c.chaindataPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading chaindata dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	loaded := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "block_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			log.Warn().Err(err).Str("file", name).Msg("Skipping unreadable block file")
			continue
		}
		var b Block
		if err := json.Unmarshal(data, &b); err != nil {
			log.Warn().Err(err).Str("file", name).Msg("Skipping malformed block file")
			continue
		}
		if b.Header.Number == 0 {
			continue
		}
		c.addBlock(&b)
		for _, tx := range b.Transactions {
			c.applyTx(tx)
		}
		loaded++
	}
	if loaded > 0 {
		log.Info().Int("blocks", loaded).Uint64("height", c.Height()).Msg("Loaded chain from disk")
	}
	return nil
}
