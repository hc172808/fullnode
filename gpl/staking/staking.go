// Package staking implements the GYDS PoS Staking Program.
// Validators register a stake; delegators bond GYD to validators;
// rewards accrue every epoch and are claimable.  Slashing reduces
// both the validator's own stake and proportional delegator stakes.
package staking

import (
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
	ErrValidatorNotFound  = errors.New("staking: validator not found")
	ErrAlreadyRegistered  = errors.New("staking: validator already registered")
	ErrBelowMinStake      = errors.New("staking: amount below minimum stake")
	ErrInsufficientStake  = errors.New("staking: insufficient staked amount")
	ErrUnauthorized       = errors.New("staking: unauthorized")
	ErrNoRewards          = errors.New("staking: no rewards available")
	ErrValidatorSlashed   = errors.New("staking: validator has been slashed and is jailed")
)

// ── Constants ─────────────────────────────────────────────────────────────────

var (
	MinValidatorStake = new(big.Int).Mul(big.NewInt(1000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) // 1000 GYD
	MinDelegation     = new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))   // 1 GYD
	RewardRate        = big.NewInt(500) // 5% APY expressed as basis points (500 bp = 5%)
	EpochLength       = uint64(100)     // blocks per epoch
	SlashRate         = big.NewInt(500) // 5% slash per infraction (basis points)
)

// ── Types ─────────────────────────────────────────────────────────────────────

type ValidatorInfo struct {
	Address       string   `json:"address"`
	SelfStake     *big.Int `json:"selfStake"`
	TotalStake    *big.Int `json:"totalStake"`
	Commission    uint64   `json:"commission"` // basis points (0-10000)
	Jailed        bool     `json:"jailed"`
	JailUntil     uint64   `json:"jailUntil"`
	SlashCount    uint64   `json:"slashCount"`
	RegisteredAt  uint64   `json:"registeredAt"`
	LastRewardAt  uint64   `json:"lastRewardAt"`
	AccumRewards  *big.Int `json:"accumRewards"`
	Uptime        uint64   `json:"uptime"` // blocks proposed
}

type Delegation struct {
	Validator    string   `json:"validator"`
	Delegator    string   `json:"delegator"`
	Amount       *big.Int `json:"amount"`
	RewardDebt   *big.Int `json:"rewardDebt"`   // already-claimed portion
	PendingReward *big.Int `json:"pendingReward"`
	DelegatedAt  int64    `json:"delegatedAt"`
}

type SlashRecord struct {
	Validator string   `json:"validator"`
	Reason    string   `json:"reason"`
	Amount    *big.Int `json:"amount"`
	BlockNum  uint64   `json:"blockNum"`
	Timestamp int64    `json:"ts"`
}

// ── Program ───────────────────────────────────────────────────────────────────

type Program struct {
	mu    sync.RWMutex
	store storage.Storage
}

func NewProgram(store storage.Storage) *Program {
	return &Program{store: store}
}

// ── Validator lifecycle ───────────────────────────────────────────────────────

// RegisterValidator registers a new validator with an initial self-stake.
func (p *Program) RegisterValidator(address string, selfStake *big.Int, commission uint64, blockNum uint64) error {
	if selfStake == nil || selfStake.Cmp(MinValidatorStake) < 0 {
		return ErrBelowMinStake
	}
	if commission > 10000 {
		return fmt.Errorf("staking: commission must be <= 10000 basis points")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if exists, _ := p.store.Has(validatorKey(address)); exists {
		return ErrAlreadyRegistered
	}

	v := ValidatorInfo{
		Address:      address,
		SelfStake:    new(big.Int).Set(selfStake),
		TotalStake:   new(big.Int).Set(selfStake),
		Commission:   commission,
		RegisteredAt: blockNum,
		AccumRewards: big.NewInt(0),
	}
	return p.saveValidator(v)
}

// AddSelfStake lets a validator increase their own stake.
func (p *Program) AddSelfStake(address string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Sign() <= 0 {
		return ErrBelowMinStake
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v, err := p.getValidator(address)
	if err != nil {
		return err
	}
	if v.Jailed {
		return ErrValidatorSlashed
	}
	v.SelfStake.Add(v.SelfStake, amount)
	v.TotalStake.Add(v.TotalStake, amount)
	return p.saveValidator(*v)
}

// ── Delegation ────────────────────────────────────────────────────────────────

// Delegate bonds amount GYD from delegator to validator.
func (p *Program) Delegate(validator, delegator string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Cmp(MinDelegation) < 0 {
		return ErrBelowMinStake
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	v, err := p.getValidator(validator)
	if err != nil {
		return err
	}
	if v.Jailed {
		return ErrValidatorSlashed
	}

	d, err := p.getDelegation(validator, delegator)
	if err != nil {
		d = &Delegation{
			Validator:     validator,
			Delegator:     delegator,
			Amount:        big.NewInt(0),
			RewardDebt:    big.NewInt(0),
			PendingReward: big.NewInt(0),
			DelegatedAt:   time.Now().Unix(),
		}
	}
	d.Amount.Add(d.Amount, amount)
	v.TotalStake.Add(v.TotalStake, amount)

	if err := p.saveValidator(*v); err != nil {
		return err
	}
	return p.saveDelegation(*d)
}

// Undelegate withdraws amount from delegator's stake in validator.
func (p *Program) Undelegate(validator, delegator string, amount *big.Int, blockNum uint64) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("staking: amount must be > 0")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	v, err := p.getValidator(validator)
	if err != nil {
		return err
	}
	d, err := p.getDelegation(validator, delegator)
	if err != nil {
		return err
	}
	if d.Amount.Cmp(amount) < 0 {
		return ErrInsufficientStake
	}

	d.Amount.Sub(d.Amount, amount)
	v.TotalStake.Sub(v.TotalStake, amount)

	if err := p.saveValidator(*v); err != nil {
		return err
	}
	return p.saveDelegation(*d)
}

// ── Rewards ───────────────────────────────────────────────────────────────────

// AccrueRewards is called at the end of each epoch to distribute block rewards.
// totalReward is the total GYD reward for this epoch.
func (p *Program) AccrueRewards(validator string, totalReward *big.Int, blockNum uint64) error {
	if totalReward == nil || totalReward.Sign() <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	v, err := p.getValidator(validator)
	if err != nil {
		return err
	}
	if v.Jailed {
		return nil
	}

	// Commission goes to validator
	commission := new(big.Int).Mul(totalReward, big.NewInt(int64(v.Commission)))
	commission.Div(commission, big.NewInt(10000))
	v.AccumRewards.Add(v.AccumRewards, commission)
	v.LastRewardAt = blockNum

	return p.saveValidator(*v)
}

// ClaimRewards transfers accumulated rewards to the claimant.
// Returns the claimable amount (caller must credit the address in chain state).
func (p *Program) ClaimRewards(validator, claimant string) (*big.Int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	v, err := p.getValidator(validator)
	if err != nil {
		return nil, err
	}

	if claimant == validator {
		if v.AccumRewards.Sign() == 0 {
			return nil, ErrNoRewards
		}
		amount := new(big.Int).Set(v.AccumRewards)
		v.AccumRewards = big.NewInt(0)
		if err := p.saveValidator(*v); err != nil {
			return nil, err
		}
		return amount, nil
	}

	d, err := p.getDelegation(validator, claimant)
	if err != nil {
		return nil, ErrNoRewards
	}
	if d.PendingReward.Sign() == 0 {
		return nil, ErrNoRewards
	}
	amount := new(big.Int).Set(d.PendingReward)
	d.PendingReward = big.NewInt(0)
	d.RewardDebt.Add(d.RewardDebt, amount)
	if err := p.saveDelegation(*d); err != nil {
		return nil, err
	}
	return amount, nil
}

// ── Slashing ──────────────────────────────────────────────────────────────────

// Slash penalises a misbehaving validator (double-sign, downtime etc.).
// amount is the GYD slashed from TotalStake; jailUntil is the block after
// which the validator may re-register.
func (p *Program) Slash(validator, reason string, amount *big.Int, jailUntil, blockNum uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	v, err := p.getValidator(validator)
	if err != nil {
		return err
	}

	slash := new(big.Int).Set(amount)
	if slash.Cmp(v.TotalStake) > 0 {
		slash.Set(v.TotalStake)
	}
	v.TotalStake.Sub(v.TotalStake, slash)
	if v.SelfStake.Cmp(slash) >= 0 {
		v.SelfStake.Sub(v.SelfStake, slash)
	} else {
		v.SelfStake = big.NewInt(0)
	}
	v.Jailed = true
	v.JailUntil = jailUntil
	v.SlashCount++

	rec := SlashRecord{
		Validator: validator,
		Reason:    reason,
		Amount:    slash,
		BlockNum:  blockNum,
		Timestamp: time.Now().Unix(),
	}
	_ = p.saveSlash(validator, rec)
	return p.saveValidator(*v)
}

// Unjail releases a validator from jail (after jailUntil block).
func (p *Program) Unjail(validator string, blockNum uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, err := p.getValidator(validator)
	if err != nil {
		return err
	}
	if blockNum < v.JailUntil {
		return fmt.Errorf("staking: still jailed until block %d", v.JailUntil)
	}
	v.Jailed = false
	return p.saveValidator(*v)
}

// ── Queries ───────────────────────────────────────────────────────────────────

func (p *Program) GetValidator(address string) (*ValidatorInfo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.getValidator(address)
}

func (p *Program) GetDelegation(validator, delegator string) (*Delegation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.getDelegation(validator, delegator)
}

func (p *Program) GetTotalStake(validator string) (*big.Int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, err := p.getValidator(validator)
	if err != nil {
		return big.NewInt(0), err
	}
	return new(big.Int).Set(v.TotalStake), nil
}

// ── Storage helpers ───────────────────────────────────────────────────────────

func validatorKey(addr string) []byte { return []byte("staking/validator/" + addr) }
func delegationKey(v, d string) []byte {
	return []byte(fmt.Sprintf("staking/delegation/%s/%s", v, d))
}
func slashKey(v string, idx uint64) []byte {
	return []byte(fmt.Sprintf("staking/slash/%s/%d", v, idx))
}

func (p *Program) getValidator(addr string) (*ValidatorInfo, error) {
	raw, err := p.store.Get(validatorKey(addr))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrValidatorNotFound
		}
		return nil, err
	}
	var v ValidatorInfo
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if v.SelfStake == nil {
		v.SelfStake = big.NewInt(0)
	}
	if v.TotalStake == nil {
		v.TotalStake = big.NewInt(0)
	}
	if v.AccumRewards == nil {
		v.AccumRewards = big.NewInt(0)
	}
	return &v, nil
}

func (p *Program) saveValidator(v ValidatorInfo) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.store.Put(validatorKey(v.Address), raw)
}

func (p *Program) getDelegation(validator, delegator string) (*Delegation, error) {
	raw, err := p.store.Get(delegationKey(validator, delegator))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("staking: delegation not found for %s→%s", delegator, validator)
		}
		return nil, err
	}
	var d Delegation
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	if d.Amount == nil {
		d.Amount = big.NewInt(0)
	}
	if d.RewardDebt == nil {
		d.RewardDebt = big.NewInt(0)
	}
	if d.PendingReward == nil {
		d.PendingReward = big.NewInt(0)
	}
	return &d, nil
}

func (p *Program) saveDelegation(d Delegation) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return p.store.Put(delegationKey(d.Validator, d.Delegator), raw)
}

func (p *Program) saveSlash(validator string, rec SlashRecord) error {
	v, _ := p.getValidator(validator)
	var idx uint64
	if v != nil {
		idx = v.SlashCount
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return p.store.Put(slashKey(validator, idx), raw)
}
