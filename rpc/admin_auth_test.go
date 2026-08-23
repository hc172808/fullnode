package rpc

import (
	"fmt"
	"testing"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpECDSA "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/gydschain/fullnode/internal/keccak"
)

func TestRecoverEthereumAddress(t *testing.T) {
	keyBytes := make([]byte, 32)
	keyBytes[31] = 7
	key := secp.PrivKeyFromBytes(keyBytes)
	message := "GYDS Chain Admin Login\n\nSign this one-time message to authenticate.\nNonce: test"
	digest := keccak.Sum256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)))
	compact := secpECDSA.SignCompact(key, digest[:], true)
	raw := make([]byte, 65)
	copy(raw[:64], compact[1:])
	raw[64] = compact[0] - 31
	recovered, err := recoverEthereumAddress(message, fmt.Sprintf("0x%x", raw))
	if err != nil {
		t.Fatal(err)
	}
	pub := key.PubKey().SerializeUncompressed()
	hash := keccak.Sum256(pub[1:])
	expected := fmt.Sprintf("0x%x", hash[12:])
	if recovered != expected {
		t.Fatalf("recovered %s, expected %s", recovered, expected)
	}
}
