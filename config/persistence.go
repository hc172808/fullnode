package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// LoadBootstrapNodes reads the durable peer list written by the admin panel.
// The file is deliberately inside the runtime data directory so Git updates
// never replace it.
func LoadBootstrapNodes(dataDir string) []string {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "admin", "bootstrap-peers.json"))
	if err != nil {
		return nil
	}
	var peers []string
	if json.Unmarshal(raw, &peers) != nil {
		return nil
	}
	return cleanPeers(peers)
}

// SaveBootstrapNodes atomically stores the durable peer list.
func SaveBootstrapNodes(dataDir string, peers []string) error {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	peers = cleanPeers(peers)
	dir := filepath.Join(dataDir, "admin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "bootstrap-peers.json.tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "bootstrap-peers.json")); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func cleanPeers(peers []string) []string {
	out := make([]string, 0, len(peers))
	seen := make(map[string]bool, len(peers))
	for _, peer := range peers {
		peer = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(peer, "tcp://"), "TCP://"))
		if peer != "" && !seen[peer] {
			seen[peer] = true
			out = append(out, peer)
		}
	}
	return out
}
