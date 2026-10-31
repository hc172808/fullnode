package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHeaderChainPersistsHeadersWithoutBlockBodiesOrEVMState(t *testing.T) {
	dataDir := t.TempDir()
	chain, err := NewHeaderChain(GydsGenesis, dataDir)
	if err != nil {
		t.Fatalf("open header chain: %v", err)
	}

	block := NewBlock(chain.Head().Header, "trusted-validator", nil)
	if err := chain.InsertHeader(block.Header); err != nil {
		t.Fatalf("insert header: %v", err)
	}
	if got := chain.Height(); got != 1 {
		t.Fatalf("height = %d, want 1", got)
	}
	stored, err := chain.GetByNumber(1)
	if err != nil {
		t.Fatalf("get inserted header: %v", err)
	}
	if stored.Transactions != nil && len(stored.Transactions) != 0 {
		t.Fatalf("header chain stored transaction bodies: %#v", stored.Transactions)
	}
	chain.Close()

	if _, err := os.Stat(filepath.Join(dataDir, "evm-state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("header chain created EVM state, stat error = %v", err)
	}

	reopened, err := NewHeaderChain(GydsGenesis, dataDir)
	if err != nil {
		t.Fatalf("reopen header chain: %v", err)
	}
	defer reopened.Close()
	if got := reopened.Height(); got != 1 {
		t.Fatalf("reopened height = %d, want 1", got)
	}
	reloaded, err := reopened.GetByNumber(1)
	if err != nil || reloaded.Header.Hash != block.Header.Hash {
		t.Fatalf("reloaded header = %#v, err=%v", reloaded, err)
	}
}

func TestHeaderChainRejectsInvalidOrNonContiguousHeaders(t *testing.T) {
	chain, err := NewHeaderChain(GydsGenesis, t.TempDir())
	if err != nil {
		t.Fatalf("open header chain: %v", err)
	}
	defer chain.Close()

	invalidHash := NewBlock(chain.Head().Header, "trusted-validator", nil).Header
	invalidHash.Hash = "0xinvalid"
	if err := chain.InsertHeader(invalidHash); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("invalid hash error = %v, want ErrInvalidHeader", err)
	}

	skipped := NewBlock(chain.Head().Header, "trusted-validator", nil).Header
	skipped.Number = 2
	skipped.Hash = skipped.ComputeHash()
	if err := chain.InsertHeader(skipped); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("non-contiguous height error = %v, want ErrInvalidHeader", err)
	}

	brokenParent := NewBlock(chain.Head().Header, "trusted-validator", nil).Header
	brokenParent.ParentHash = "0xwrong-parent"
	brokenParent.Hash = brokenParent.ComputeHash()
	if err := chain.InsertHeader(brokenParent); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("broken parent error = %v, want ErrParentNotFound", err)
	}
}
