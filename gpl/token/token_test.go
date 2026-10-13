package token

import (
	"errors"
	"math/big"
	"testing"

	"github.com/gydschain/fullnode/storage"
)

func newTestProgram(t *testing.T) *Program {
	t.Helper()
	return NewProgram(storage.NewMemStorage())
}

func createTestToken(t *testing.T, p *Program) TokenID {
	t.Helper()
	id, err := p.CreateToken(
		"Test Token", "TST", 18, big.NewInt(100),
		"creator", "mint-1", "freeze-1", "", 1,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	<-p.Events() // creation event
	return id
}

func TestAuthorityTransfersAndRevocationsEmitEvents(t *testing.T) {
	p := newTestProgram(t)
	id := createTestToken(t, p)

	if err := p.SetMintAuthority(id, "mint-1", "mint-2", 7); err != nil {
		t.Fatalf("SetMintAuthority transfer: %v", err)
	}
	assertAuthorityEvent(t, <-p.Events(), id, "SetMintAuthority", "mint-1", "mint-2", 7)

	if err := p.SetMintAuthority(id, "mint-1", "", 8); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old mint authority error = %v, want ErrUnauthorized", err)
	}
	if err := p.SetMintAuthority(id, "mint-2", "", 8); err != nil {
		t.Fatalf("SetMintAuthority revoke: %v", err)
	}
	assertAuthorityEvent(t, <-p.Events(), id, "SetMintAuthority", "mint-2", "", 8)
	if err := p.SetMintAuthority(id, "", "mint-3", 9); !errors.Is(err, ErrMintAuthorityFixed) {
		t.Fatalf("restore revoked mint authority error = %v, want ErrMintAuthorityFixed", err)
	}

	if err := p.SetFreezeAuthority(id, "freeze-1", "freeze-2", 10); err != nil {
		t.Fatalf("SetFreezeAuthority transfer: %v", err)
	}
	assertAuthorityEvent(t, <-p.Events(), id, "SetFreezeAuthority", "freeze-1", "freeze-2", 10)
	if err := p.SetFreezeAuthority(id, "freeze-2", "", 11); err != nil {
		t.Fatalf("SetFreezeAuthority revoke: %v", err)
	}
	assertAuthorityEvent(t, <-p.Events(), id, "SetFreezeAuthority", "freeze-2", "", 11)
	if err := p.SetFreezeAuthority(id, "", "freeze-3", 12); !errors.Is(err, ErrFreezeAuthorityFixed) {
		t.Fatalf("restore revoked freeze authority error = %v, want ErrFreezeAuthorityFixed", err)
	}
}

func TestFreezeAndUnfreezeEmitEventsAndPersistState(t *testing.T) {
	p := newTestProgram(t)
	id := createTestToken(t, p)

	if err := p.FreezeAccount(id, "freeze-1", "holder", true, 21); err != nil {
		t.Fatalf("FreezeAccount: %v", err)
	}
	assertAuthorityEvent(t, <-p.Events(), id, "Freeze", "freeze-1", "holder", 21)
	account, err := p.getAccount(id, "holder")
	if err != nil {
		t.Fatalf("get frozen account: %v", err)
	}
	if !account.Frozen {
		t.Fatal("account is not frozen after FreezeAccount")
	}

	if err := p.FreezeAccount(id, "wrong-authority", "holder", false, 22); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthorized Unfreeze error = %v, want ErrUnauthorized", err)
	}
	if err := p.FreezeAccount(id, "freeze-1", "holder", false, 23); err != nil {
		t.Fatalf("Unfreeze account: %v", err)
	}
	assertAuthorityEvent(t, <-p.Events(), id, "Unfreeze", "freeze-1", "holder", 23)
	account, err = p.getAccount(id, "holder")
	if err != nil {
		t.Fatalf("get unfrozen account: %v", err)
	}
	if account.Frozen {
		t.Fatal("account is still frozen after Unfreeze")
	}
}

func assertAuthorityEvent(t *testing.T, event EventLog, tokenID TokenID, name, from, to string, blockNum uint64) {
	t.Helper()
	if event.TokenID != tokenID || event.Event != name || event.From != from ||
		event.To != to || event.BlockNum != blockNum || event.Authority != from {
		t.Fatalf("event = %+v, want token=%s event=%s from=%q to=%q block=%d authority=%q",
			event, tokenID, name, from, to, blockNum, from)
	}
}
