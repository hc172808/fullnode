package rpc

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupApplyDoesNotEchoWalletKey(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, ".env")
	dataDir := filepath.Join(root, "data")
	t.Setenv("GYDS_ENV_FILE", envPath)

	const testKey = "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	body, err := json.Marshal(map[string]string{
		"network":       "testnet",
		"nodeMode":      "full",
		"dataDir":       dataDir,
		"walletKey":     testKey,
		"walletAddress": "0x0000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	(&Server{dataDir: dataDir}).handleSetupApply(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("setup apply status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), testKey) {
		t.Fatal("setup response exposed the submitted wallet key")
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	if _, ok := response["env"]; ok {
		t.Fatal("setup response must not include the .env contents")
	}

	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read generated test .env: %v", err)
	}
	if !bytes.Contains(envBytes, []byte(testKey)) {
		t.Fatal("setup did not preserve the optional server wallet key in .env")
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat generated test .env: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf(".env permissions = %o; want 600", info.Mode().Perm())
	}
}

func TestWalletSetupPagesWarnAboutServerKeyStorage(t *testing.T) {
	for _, path := range []string{"static/setup.html", "static/admin-node.html"} {
		file, err := staticFiles.Open(path)
		if err != nil {
			t.Fatalf("open embedded %s: %v", path, err)
		}
		content, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			t.Fatalf("read embedded %s: %v", path, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close embedded %s: %v", path, closeErr)
		}
		text := string(content)
		for _, warning := range []string{
			"key is sent to this server and stored in",
			"The app does not encrypt it",
			"Never enter a personal, admin-login, validator, or GYD treasury key",
		} {
			if !strings.Contains(text, warning) {
				t.Errorf("%s is missing warning text %q", path, warning)
			}
		}
	}
}
