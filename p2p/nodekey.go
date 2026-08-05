package p2p

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NodeKey holds the ed25519 signing keypair that permanently identifies this node
// on the GYDS P2P network. The Node ID (hex-encoded public key, 64 chars) is
// shared with peer operators who want to whitelist this node.
type NodeKey struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	id   string // hex(pub) — 64 hex chars
}

// LoadOrCreateNodeKey loads the node keypair from <dataDir>/node.key, creating and
// persisting a new one if the file does not exist.
func LoadOrCreateNodeKey(dataDir string) (*NodeKey, error) {
	path := filepath.Join(dataDir, "node.key")
	raw, err := os.ReadFile(path)
	if err == nil {
		seed, decErr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if decErr != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("node.key at %s is corrupt (expected %d-byte hex seed)", path, ed25519.SeedSize)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		return &NodeKey{priv: priv, pub: pub, id: hex.EncodeToString(pub)}, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read node.key: %w", err)
	}

	// Generate a fresh keypair and persist it.
	pub, priv, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		return nil, fmt.Errorf("generate node keypair: %w", genErr)
	}
	if mkErr := os.MkdirAll(dataDir, 0700); mkErr != nil {
		return nil, fmt.Errorf("create data dir: %w", mkErr)
	}
	seed := priv.Seed()
	if wErr := os.WriteFile(path, []byte(hex.EncodeToString(seed)+"\n"), 0600); wErr != nil {
		return nil, fmt.Errorf("save node.key: %w", wErr)
	}
	return &NodeKey{priv: priv, pub: pub, id: hex.EncodeToString(pub)}, nil
}

// ID returns the hex-encoded ed25519 public key used as this node's identity.
// Share this with peer operators so they can whitelist you.
func (nk *NodeKey) ID() string { return nk.id }

// Sign signs data with the node's ed25519 private key and returns the hex-encoded
// 64-byte signature. Used during the peer auth handshake.
func (nk *NodeKey) Sign(data []byte) string {
	return hex.EncodeToString(ed25519.Sign(nk.priv, data))
}

// VerifyNodeSig checks whether hexSig is a valid ed25519 signature of data made
// by the node whose ID is hexNodeID. Returns false on any parse or verify error.
func VerifyNodeSig(hexNodeID string, data []byte, hexSig string) bool {
	pub, err := hex.DecodeString(hexNodeID)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(hexSig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, data, sig)
}
