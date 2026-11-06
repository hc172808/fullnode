package rpc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestAdminLoginRejectsWrongWalletBeforeConsumingChallenge(t *testing.T) {
	auth := NewAuthStore(t.TempDir())
	nonce, _ := auth.NewChallenge()
	s := &Server{auth: auth}
	body := fmt.Sprintf(`{"address":"0x%s","nonce":%q,"signature":"","pin":"2468"}`, strings.Repeat("0", 40), nonce)
	request := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(body))
	response := httptest.NewRecorder()

	s.handleAdminLoginSubmit(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "not the authorized admin wallet") {
		t.Fatalf("wrong-wallet login response = %d %s", response.Code, response.Body.String())
	}
	if _, ok := auth.ConsumeChallenge(nonce); !ok {
		t.Fatal("wrong-wallet attempt consumed the challenge before signing")
	}
}

func TestAdminLoginPageChecksWalletBeforePersonalSign(t *testing.T) {
	page, err := os.ReadFile("static/admin-login.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(page)
	loginStart := strings.Index(source, "async function doLogin()")
	recoveryStart := strings.Index(source, "async function recoverPIN()")
	if loginStart < 0 || recoveryStart <= loginStart {
		t.Fatal("could not locate the login handler in admin-login.html")
	}
	login := source[loginStart:recoveryStart]
	walletCheck := strings.Index(login, "if (!sameAddress(address, expectedWallet))")
	accountRecheck := strings.Index(login, "const currentAccounts = await window.ethereum.request({ method: 'eth_accounts' })")
	personalSign := strings.Index(login, "method: 'personal_sign'")
	if walletCheck < 0 || accountRecheck < 0 || personalSign < 0 ||
		walletCheck > personalSign || accountRecheck > personalSign {
		t.Fatal("login must reject the wrong wallet and recheck the active account before personal_sign")
	}
	if !strings.Contains(login[walletCheck:personalSign], "No signature was requested.") {
		t.Fatal("wrong-wallet diagnostic must explicitly say no signature was requested")
	}
}
