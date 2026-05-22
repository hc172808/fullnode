package consensus

import (
	"math/rand"
	"sync"
	"time"

	"github.com/gydschain/litenode/core"
)

type ValidatorSet struct {
	mu         sync.RWMutex
	validators []string
	stakes     map[string]int64
}

func NewValidatorSet(validators []string) *ValidatorSet {
	vs := &ValidatorSet{
		validators: validators,
		stakes:     make(map[string]int64),
	}
	for _, v := range validators {
		vs.stakes[v] = 1_000_000
	}
	return vs
}

func (vs *ValidatorSet) SelectProposer(blockNum uint64) string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	if len(vs.validators) == 0 {
		return "0x0000000000000000000000000000000000000001"
	}
	idx := int(blockNum) % len(vs.validators)
	return vs.validators[idx]
}

func (vs *ValidatorSet) AddValidator(addr string, stake int64) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.validators = append(vs.validators, addr)
	vs.stakes[addr] = stake
}

func (vs *ValidatorSet) Validators() []string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	out := make([]string, len(vs.validators))
	copy(out, vs.validators)
	return out
}

type PoSEngine struct {
	vs          *ValidatorSet
	chain       *core.Chain
	blockTime   time.Duration
	quit        chan struct{}
	newBlockFn  func(*core.Block)
	rng         *rand.Rand
}

func NewPoSEngine(chain *core.Chain, vs *ValidatorSet, blockTime time.Duration) *PoSEngine {
	return &PoSEngine{
		vs:        vs,
		chain:     chain,
		blockTime: blockTime,
		quit:      make(chan struct{}),
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (e *PoSEngine) OnNewBlock(fn func(*core.Block)) {
	e.newBlockFn = fn
}

func (e *PoSEngine) Start() {
	go e.loop()
}

func (e *PoSEngine) Stop() {
	close(e.quit)
}

func (e *PoSEngine) loop() {
	ticker := time.NewTicker(e.blockTime)
	defer ticker.Stop()
	for {
		select {
		case <-e.quit:
			return
		case <-ticker.C:
			e.produceBlock()
		}
	}
}

func (e *PoSEngine) produceBlock() {
	head := e.chain.Head()
	if head == nil {
		return
	}
	nextNum := head.Header.Number + 1
	proposer := e.vs.SelectProposer(nextNum)

	txCount := e.rng.Intn(8)
	txs := make([]*core.Transaction, txCount)
	for i := range txs {
		from := proposer
		to := e.vs.SelectProposer(nextNum + uint64(i+1))
		txs[i] = core.NewTransaction(from, to, nil, uint64(i), nil)
		txs[i].Status = "success"
		txs[i].BlockNum = nextNum
	}

	blk := core.NewBlock(head.Header, proposer, txs)
	if err := e.chain.InsertBlock(blk); err != nil {
		return
	}
	if e.newBlockFn != nil {
		e.newBlockFn(blk)
	}
}
