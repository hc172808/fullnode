// Package token implements the GYDS-20 shared Token Program — a single
// on-chain engine that manages every fungible token on the GYDS chain.
// Inspired by Solana's SPL Token Program: tokens are NOT separate contracts;
// they are accounts owned by this program and operated via typed instructions.
package token

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/gydschain/litenode/storage"
)

// ── Errors ───────────────────────────────────────────────────────────────────

var (
	ErrTokenNotFound      = errors.New("token: token not found")
	ErrAccountNotFound    = errors.New("token: token account not found")
	ErrInsufficientFunds  = errors.New("token: insufficient funds")
	ErrUnauthorized       = errors.New("token: unauthorized")
	ErrInvalidAmount      = errors.New("token: amount must be > 0")
	ErrMintAuthorityFixed = errors.New("token: mint authority is frozen")
	ErrOverflow           = errors.New("token: arithmetic overflow")
	ErrBadDecimals        = errors.New("token: decimals must be <= 18")
	ErrAllowanceExceeded  = errors.New("token: transfer amount exceeds allowance")
)

// ── Types ────────────────────────────────────────────────────────────────────

// TokenID is a deterministic identifier derived from (creator, symbol, nonce).
type TokenID = string

// TokenInfo describes an on-chain GYDS-20 token.
type TokenInfo struct {
	ID            TokenID  `json:"id"`
	Name          string   `json:"name"`
	Symbol        string   `json:"symbol"`
	Decimals      uint8    `json:"decimals"`
	TotalSupply   *big.Int `json:"totalSupply"`
	MintAuthority string   `json:"mintAuthority"`    // "" means mint is frozen
	FreezeAuth    string   `json:"freezeAuthority"` // "" means no freeze
	Creator       string   `json:"creator"`
	CreatedAt     int64    `json:"createdAt"`
	URI           string   `json:"uri,omitempty"` // metadata URI (IPFS / HTTPS)
}

// TokenAccount holds a user's balance for a specific token.
type TokenAccount struct {
	TokenID  TokenID  `json:"tokenId"`
	Owner    string   `json:"owner"`
	Balance  *big.Int `json:"balance"`
	Frozen   bool     `json:"frozen"`
	Delegate string   `json:"delegate,omitempty"`
	Allowance *big.Int `json:"allowance,omitempty"`
}

// EventLog is emitted for every state change.
type EventLog struct {
	TokenID   TokenID  `json:"tokenId"`
	Event     string   `json:"event"` // Transfer, Mint, Burn, Approve, Freeze
	From      string   `json:"from,omitempty"`
	To        string   `json:"to,omitempty"`
	Amount    *big.Int `json:"amount,omitempty"`
	Authority string   `json:"authority,omitempty"`
	BlockNum  uint64   `json:"blockNum"`
	TxHash    string   `json:"txHash,omitempty"`
	Timestamp int64    `json:"ts"`
}

// ── Program ──────────────────────────────────────────────────────────────────

// Program is the singleton GYDS-20 Token Program.
type Program struct {
	mu      sync.RWMutex
	store   storage.Storage
	eventCh chan EventLog
}

// NewProgram creates (or reopens) the token program backed by store.
func NewProgram(store storage.Storage) *Program {
	p := &Program{
		store:   store,
		eventCh: make(chan EventLog, 256),
	}
	return p
}

// Events returns the channel on which state-change logs are published.
func (p *Program) Events() <-chan EventLog { return p.eventCh }

// ── Token lifecycle ───────────────────────────────────────────────────────────

// CreateToken mints a new token type and pre-allocates supply to initialOwner.
func (p *Program) CreateToken(
	name, symbol string,
	decimals uint8,
	initialSupply *big.Int,
	creator, mintAuthority, freezeAuthority, uri string,
	blockNum uint64,
) (TokenID, error) {
	if decimals > 18 {
		return "", ErrBadDecimals
	}
	if initialSupply == nil || initialSupply.Sign() < 0 {
		return "", ErrInvalidAmount
	}

	id := deriveTokenID(creator, symbol, blockNum)

	p.mu.Lock()
	defer p.mu.Unlock()

	if exists, _ := p.store.Has(tokenKey(id)); exists {
		return "", fmt.Errorf("token: token %s already exists", id)
	}

	info := TokenInfo{
		ID:            id,
		Name:          name,
		Symbol:        symbol,
		Decimals:      decimals,
		TotalSupply:   new(big.Int).Set(initialSupply),
		MintAuthority: mintAuthority,
		FreezeAuth:    freezeAuthority,
		Creator:       creator,
		CreatedAt:     time.Now().Unix(),
		URI:           uri,
	}
	if err := p.saveToken(info); err != nil {
		return "", err
	}

	// Pre-allocate supply to creator
	if initialSupply.Sign() > 0 {
		acc := TokenAccount{
			TokenID: id,
			Owner:   creator,
			Balance: new(big.Int).Set(initialSupply),
		}
		if err := p.saveAccount(acc); err != nil {
			return "", err
		}
	}

	p.emit(EventLog{
		TokenID:   id,
		Event:     "Create",
		To:        creator,
		Amount:    initialSupply,
		Authority: creator,
		BlockNum:  blockNum,
		Timestamp: time.Now().Unix(),
	})
	return id, nil
}

// ── Core operations ───────────────────────────────────────────────────────────

// Transfer moves amount of tokenId from → to.
func (p *Program) Transfer(tokenId, from, to string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Sign() <= 0 {
		return ErrInvalidAmount
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	fromAcc, err := p.getAccount(tokenId, from)
	if err != nil {
		return err
	}
	if fromAcc.Frozen {
		return fmt.Errorf("token: sender account frozen")
	}
	if fromAcc.Balance.Cmp(amount) < 0 {
		return ErrInsufficientFunds
	}

	toAcc, err := p.getOrCreateAccount(tokenId, to)
	if err != nil {
		return err
	}
	if toAcc.Frozen {
		return fmt.Errorf("token: recipient account frozen")
	}

	fromAcc.Balance.Sub(fromAcc.Balance, amount)
	toAcc.Balance.Add(toAcc.Balance, amount)

	if err := p.saveAccount(*fromAcc); err != nil {
		return err
	}
	if err := p.saveAccount(*toAcc); err != nil {
		return err
	}

	p.emit(EventLog{
		TokenID:  tokenId,
		Event:    "Transfer",
		From:     from,
		To:       to,
		Amount:   new(big.Int).Set(amount),
		BlockNum: blockNum,
		Timestamp: time.Now().Unix(),
	})
	return nil
}

// Mint creates new tokens and assigns them to `to`. Requires mintAuthority.
func (p *Program) Mint(tokenId, authority, to string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Sign() <= 0 {
		return ErrInvalidAmount
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	info, err := p.getToken(tokenId)
	if err != nil {
		return err
	}
	if info.MintAuthority == "" {
		return ErrMintAuthorityFixed
	}
	if info.MintAuthority != authority {
		return ErrUnauthorized
	}

	toAcc, err := p.getOrCreateAccount(tokenId, to)
	if err != nil {
		return err
	}
	toAcc.Balance.Add(toAcc.Balance, amount)
	info.TotalSupply.Add(info.TotalSupply, amount)

	if err := p.saveAccount(*toAcc); err != nil {
		return err
	}
	if err := p.saveToken(*info); err != nil {
		return err
	}

	p.emit(EventLog{
		TokenID:   tokenId,
		Event:     "Mint",
		To:        to,
		Amount:    new(big.Int).Set(amount),
		Authority: authority,
		BlockNum:  blockNum,
		Timestamp: time.Now().Unix(),
	})
	return nil
}

// Burn destroys amount tokens from `from`. Requires authority == mintAuthority
// OR authority == from (self-burn).
func (p *Program) Burn(tokenId, authority, from string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Sign() <= 0 {
		return ErrInvalidAmount
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	info, err := p.getToken(tokenId)
	if err != nil {
		return err
	}
	if authority != from && authority != info.MintAuthority {
		return ErrUnauthorized
	}

	fromAcc, err := p.getAccount(tokenId, from)
	if err != nil {
		return err
	}
	if fromAcc.Balance.Cmp(amount) < 0 {
		return ErrInsufficientFunds
	}

	fromAcc.Balance.Sub(fromAcc.Balance, amount)
	info.TotalSupply.Sub(info.TotalSupply, amount)

	if err := p.saveAccount(*fromAcc); err != nil {
		return err
	}
	if err := p.saveToken(*info); err != nil {
		return err
	}

	p.emit(EventLog{
		TokenID:   tokenId,
		Event:     "Burn",
		From:      from,
		Amount:    new(big.Int).Set(amount),
		Authority: authority,
		BlockNum:  blockNum,
		Timestamp: time.Now().Unix(),
	})
	return nil
}

// Approve sets a delegate allowance.
func (p *Program) Approve(tokenId, owner, delegate string, amount *big.Int, blockNum uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	acc, err := p.getAccount(tokenId, owner)
	if err != nil {
		return err
	}
	acc.Delegate = delegate
	if amount == nil {
		acc.Allowance = big.NewInt(0)
	} else {
		acc.Allowance = new(big.Int).Set(amount)
	}
	if err := p.saveAccount(*acc); err != nil {
		return err
	}
	p.emit(EventLog{
		TokenID:   tokenId,
		Event:     "Approve",
		From:      owner,
		To:        delegate,
		Amount:    acc.Allowance,
		BlockNum:  blockNum,
		Timestamp: time.Now().Unix(),
	})
	return nil
}

// TransferFrom transfers on behalf of owner using an approved allowance.
func (p *Program) TransferFrom(tokenId, delegate, from, to string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Sign() <= 0 {
		return ErrInvalidAmount
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	fromAcc, err := p.getAccount(tokenId, from)
	if err != nil {
		return err
	}
	if fromAcc.Delegate != delegate {
		return ErrUnauthorized
	}
	if fromAcc.Allowance == nil || fromAcc.Allowance.Cmp(amount) < 0 {
		return ErrAllowanceExceeded
	}
	if fromAcc.Balance.Cmp(amount) < 0 {
		return ErrInsufficientFunds
	}

	toAcc, err := p.getOrCreateAccount(tokenId, to)
	if err != nil {
		return err
	}

	fromAcc.Balance.Sub(fromAcc.Balance, amount)
	fromAcc.Allowance.Sub(fromAcc.Allowance, amount)
	toAcc.Balance.Add(toAcc.Balance, amount)

	if err := p.saveAccount(*fromAcc); err != nil {
		return err
	}
	if err := p.saveAccount(*toAcc); err != nil {
		return err
	}

	p.emit(EventLog{
		TokenID:   tokenId,
		Event:     "TransferFrom",
		From:      from,
		To:        to,
		Amount:    new(big.Int).Set(amount),
		Authority: delegate,
		BlockNum:  blockNum,
		Timestamp: time.Now().Unix(),
	})
	return nil
}

// FreezeAccount freezes or unfreezes a token account.
func (p *Program) FreezeAccount(tokenId, freezeAuth, target string, freeze bool, blockNum uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, err := p.getToken(tokenId)
	if err != nil {
		return err
	}
	if info.FreezeAuth == "" || info.FreezeAuth != freezeAuth {
		return ErrUnauthorized
	}

	acc, err := p.getOrCreateAccount(tokenId, target)
	if err != nil {
		return err
	}
	acc.Frozen = freeze
	return p.saveAccount(*acc)
}

// SetMintAuthority transfers or revokes mint authority.
func (p *Program) SetMintAuthority(tokenId, currentAuth, newAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	info, err := p.getToken(tokenId)
	if err != nil {
		return err
	}
	if info.MintAuthority != currentAuth {
		return ErrUnauthorized
	}
	info.MintAuthority = newAuth
	return p.saveToken(*info)
}

// ── Queries ──────────────────────────────────────────────────────────────────

func (p *Program) GetTokenInfo(tokenId TokenID) (*TokenInfo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.getToken(tokenId)
}

func (p *Program) GetBalance(tokenId, owner string) (*big.Int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	acc, err := p.getAccount(tokenId, owner)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return big.NewInt(0), nil
		}
		return nil, err
	}
	return new(big.Int).Set(acc.Balance), nil
}

func (p *Program) GetAllowance(tokenId, owner, spender string) (*big.Int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	acc, err := p.getAccount(tokenId, owner)
	if err != nil {
		return big.NewInt(0), nil
	}
	if acc.Delegate != spender || acc.Allowance == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(acc.Allowance), nil
}

// ── Storage helpers ───────────────────────────────────────────────────────────

func tokenKey(id TokenID) []byte     { return []byte("token/info/" + id) }
func accountKey(tid, owner string) []byte {
	return []byte(fmt.Sprintf("token/acc/%s/%s", tid, owner))
}

func (p *Program) getToken(id TokenID) (*TokenInfo, error) {
	raw, err := p.store.Get(tokenKey(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	var info TokenInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	if info.TotalSupply == nil {
		info.TotalSupply = big.NewInt(0)
	}
	return &info, nil
}

func (p *Program) saveToken(info TokenInfo) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return p.store.Put(tokenKey(info.ID), raw)
}

func (p *Program) getAccount(tid, owner string) (*TokenAccount, error) {
	raw, err := p.store.Get(accountKey(tid, owner))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrAccountNotFound
		}
		return nil, err
	}
	var acc TokenAccount
	if err := json.Unmarshal(raw, &acc); err != nil {
		return nil, err
	}
	if acc.Balance == nil {
		acc.Balance = big.NewInt(0)
	}
	return &acc, nil
}

func (p *Program) getOrCreateAccount(tid, owner string) (*TokenAccount, error) {
	acc, err := p.getAccount(tid, owner)
	if errors.Is(err, ErrAccountNotFound) {
		return &TokenAccount{
			TokenID: tid,
			Owner:   owner,
			Balance: big.NewInt(0),
		}, nil
	}
	return acc, err
}

func (p *Program) saveAccount(acc TokenAccount) error {
	raw, err := json.Marshal(acc)
	if err != nil {
		return err
	}
	return p.store.Put(accountKey(acc.TokenID, acc.Owner), raw)
}

func (p *Program) emit(e EventLog) {
	select {
	case p.eventCh <- e:
	default:
	}
}

// deriveTokenID creates a deterministic ID from creator, symbol, and block.
func deriveTokenID(creator, symbol string, blockNum uint64) TokenID {
	raw := fmt.Sprintf("%s:%s:%d", creator, symbol, blockNum)
	sum := sha256.Sum256([]byte(raw))
	return "0x" + hex.EncodeToString(sum[:16])
}
