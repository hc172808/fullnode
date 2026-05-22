// Package vm implements the GYDS deterministic, gas-metered smart contract VM.
// It is a simple stack-based bytecode interpreter with 32-byte words,
// upgradeable via module registration, and sandboxed from the host.
//
// Opcode layout: single byte opcode, followed by optional immediate operands.
// Gas costs are inspired by EVM but simplified.
package vm

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// ── Errors ────────────────────────────────────────────────────────────────────

var (
	ErrOutOfGas        = errors.New("vm: out of gas")
	ErrStackOverflow   = errors.New("vm: stack overflow")
	ErrStackUnderflow  = errors.New("vm: stack underflow")
	ErrInvalidOpcode   = errors.New("vm: invalid opcode")
	ErrInvalidJump     = errors.New("vm: invalid jump destination")
	ErrExecutionReverted = errors.New("vm: execution reverted")
	ErrMemoryLimit     = errors.New("vm: memory limit exceeded")
	ErrCodeTooLarge    = errors.New("vm: code exceeds maximum size")
)

// ── Opcodes ───────────────────────────────────────────────────────────────────

type OpCode byte

const (
	STOP   OpCode = 0x00
	ADD    OpCode = 0x01
	SUB    OpCode = 0x02
	MUL    OpCode = 0x03
	DIV    OpCode = 0x04
	MOD    OpCode = 0x05
	LT     OpCode = 0x06
	GT     OpCode = 0x07
	EQ     OpCode = 0x08
	AND    OpCode = 0x09
	OR     OpCode = 0x0a
	NOT    OpCode = 0x0b
	XOR    OpCode = 0x0c
	SHL    OpCode = 0x0d
	SHR    OpCode = 0x0e
	PUSH1  OpCode = 0x10 // push 1-byte immediate
	PUSH32 OpCode = 0x11 // push 32-byte immediate
	POP    OpCode = 0x12
	DUP    OpCode = 0x13 // dup top
	SWAP   OpCode = 0x14 // swap top two
	MLOAD  OpCode = 0x20 // load 32 bytes from memory
	MSTORE OpCode = 0x21 // store 32 bytes to memory
	SLOAD  OpCode = 0x30 // load from contract storage
	SSTORE OpCode = 0x31 // store to contract storage
	JUMP   OpCode = 0x40
	JUMPI  OpCode = 0x41 // conditional jump
	JUMPDEST OpCode = 0x42
	PC     OpCode = 0x43 // current program counter
	GAS    OpCode = 0x44 // remaining gas
	CALL   OpCode = 0x50 // inter-contract call (stubbed)
	RETURN OpCode = 0x51
	REVERT OpCode = 0x52
	LOG0   OpCode = 0x60 // emit log (0 topics)
	LOG1   OpCode = 0x61 // emit log (1 topic)
	ORIGIN OpCode = 0x70 // tx.origin
	CALLER OpCode = 0x71 // msg.sender
	CALLVALUE OpCode = 0x72 // msg.value
	ADDRESS   OpCode = 0x73 // this contract address
	BALANCE   OpCode = 0x74 // balance of address on stack
	BLOCKNUMBER OpCode = 0x75
	TIMESTAMP   OpCode = 0x76
	SHA3        OpCode = 0x80
)

// gasCost returns the base gas cost for an opcode.
func gasCost(op OpCode) uint64 {
	switch op {
	case STOP, RETURN, REVERT:
		return 0
	case ADD, SUB, LT, GT, EQ, AND, OR, NOT, XOR, SHL, SHR:
		return 3
	case MUL, DIV, MOD:
		return 5
	case PUSH1, PUSH32, POP, DUP, SWAP:
		return 3
	case MLOAD, MSTORE:
		return 3
	case SLOAD:
		return 200
	case SSTORE:
		return 5000
	case JUMP, JUMPI, JUMPDEST:
		return 8
	case LOG0:
		return 375
	case LOG1:
		return 750
	case SHA3:
		return 30
	case CALL:
		return 700
	default:
		return 1
	}
}

// ── Execution Context ─────────────────────────────────────────────────────────

// Context provides host-visible information available during execution.
type Context struct {
	Origin      string   // transaction origin
	Caller      string   // msg.sender
	ContractAddr string  // address of this contract
	Value       *big.Int // msg.value in wei
	GasPrice    *big.Int
	BlockNumber uint64
	Timestamp   int64
	// Callbacks into the host chain
	GetBalance  func(addr string) *big.Int
	EmitLog     func(contractAddr string, topics [][]byte, data []byte)
}

// ── Contract State ────────────────────────────────────────────────────────────

// ContractAccount holds the code and persistent storage of a deployed contract.
type ContractAccount struct {
	Address  string            `json:"address"`
	Code     []byte            `json:"code"`
	Storage  map[string]string `json:"storage"` // hex key → hex value
	Balance  *big.Int          `json:"balance"`
	CodeHash string            `json:"codeHash"`
	Nonce    uint64            `json:"nonce"`
}

// StorageGet retrieves a 32-byte word from contract storage.
func (ca *ContractAccount) StorageGet(key []byte) []byte {
	if ca.Storage == nil {
		return make([]byte, 32)
	}
	v, ok := ca.Storage[hex.EncodeToString(key)]
	if !ok {
		return make([]byte, 32)
	}
	raw, _ := hex.DecodeString(v)
	if len(raw) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(raw):], raw)
		return padded
	}
	return raw[:32]
}

// StorageSet stores a 32-byte word in contract storage.
func (ca *ContractAccount) StorageSet(key, value []byte) {
	if ca.Storage == nil {
		ca.Storage = make(map[string]string)
	}
	ca.Storage[hex.EncodeToString(key)] = hex.EncodeToString(value)
}

// ── Execution Log ─────────────────────────────────────────────────────────────

type Log struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// ── Result ────────────────────────────────────────────────────────────────────

type ExecutionResult struct {
	Success    bool
	ReturnData []byte
	GasUsed    uint64
	Logs       []Log
	Error      error
}

// ── Machine ───────────────────────────────────────────────────────────────────

const (
	MaxStackDepth = 1024
	MaxMemory     = 64 * 1024  // 64 KB
	MaxCodeSize   = 24 * 1024  // 24 KB
	MaxGas        = 10_000_000 // per call
)

type machine struct {
	code     []byte
	stack    []*big.Int
	memory   []byte
	pc       int
	gasLeft  uint64
	gasUsed  uint64
	ctx      Context
	contract *ContractAccount
	logs     []Log
	returnData []byte
}

// Execute runs code against contract with the given gas limit.
func Execute(ctx Context, contract *ContractAccount, input []byte, gasLimit uint64) ExecutionResult {
	if gasLimit > MaxGas {
		gasLimit = MaxGas
	}
	if len(contract.Code) > MaxCodeSize {
		return ExecutionResult{Error: ErrCodeTooLarge}
	}
	m := &machine{
		code:     contract.Code,
		memory:   make([]byte, 0, 256),
		gasLeft:  gasLimit,
		ctx:      ctx,
		contract: contract,
	}
	err := m.run()
	return ExecutionResult{
		Success:    err == nil,
		ReturnData: m.returnData,
		GasUsed:    gasLimit - m.gasLeft,
		Logs:       m.logs,
		Error:      err,
	}
}

// DeployCode validates and stores deployment bytecode, returning the contract hash.
func DeployCode(code []byte, deployer string, nonce uint64) (*ContractAccount, error) {
	if len(code) > MaxCodeSize {
		return nil, ErrCodeTooLarge
	}
	addrRaw := fmt.Sprintf("%s:%d", deployer, nonce)
	sum := sha256.Sum256([]byte(addrRaw))
	addr := "0x" + hex.EncodeToString(sum[:20])

	codeHash := sha256.Sum256(code)
	return &ContractAccount{
		Address:  addr,
		Code:     code,
		Storage:  make(map[string]string),
		Balance:  big.NewInt(0),
		CodeHash: hex.EncodeToString(codeHash[:]),
		Nonce:    0,
	}, nil
}

// ── Instruction execution ─────────────────────────────────────────────────────

func (m *machine) run() error {
	for m.pc < len(m.code) {
		op := OpCode(m.code[m.pc])

		cost := gasCost(op)
		if m.gasLeft < cost {
			return ErrOutOfGas
		}
		m.gasLeft -= cost
		m.gasUsed += cost

		switch op {
		case STOP:
			return nil

		case ADD:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			m.push(new(big.Int).Add(a, b))

		case SUB:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			result := new(big.Int).Sub(a, b)
			m.push(result)

		case MUL:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			m.push(new(big.Int).Mul(a, b))

		case DIV:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			if b.Sign() == 0 {
				m.push(big.NewInt(0))
			} else {
				m.push(new(big.Int).Div(a, b))
			}

		case MOD:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			if b.Sign() == 0 {
				m.push(big.NewInt(0))
			} else {
				m.push(new(big.Int).Mod(a, b))
			}

		case LT:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			if a.Cmp(b) < 0 {
				m.push(big.NewInt(1))
			} else {
				m.push(big.NewInt(0))
			}

		case GT:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			if a.Cmp(b) > 0 {
				m.push(big.NewInt(1))
			} else {
				m.push(big.NewInt(0))
			}

		case EQ:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			if a.Cmp(b) == 0 {
				m.push(big.NewInt(1))
			} else {
				m.push(big.NewInt(0))
			}

		case AND:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			m.push(new(big.Int).And(a, b))

		case OR:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			m.push(new(big.Int).Or(a, b))

		case XOR:
			a, b, err := m.pop2()
			if err != nil {
				return err
			}
			m.push(new(big.Int).Xor(a, b))

		case NOT:
			a, err := m.pop()
			if err != nil {
				return err
			}
			m.push(new(big.Int).Not(a))

		case SHL:
			shift, val, err := m.pop2()
			if err != nil {
				return err
			}
			s := uint(shift.Uint64())
			m.push(new(big.Int).Lsh(val, s))

		case SHR:
			shift, val, err := m.pop2()
			if err != nil {
				return err
			}
			s := uint(shift.Uint64())
			m.push(new(big.Int).Rsh(val, s))

		case PUSH1:
			if m.pc+1 >= len(m.code) {
				return ErrInvalidOpcode
			}
			m.pc++
			m.push(big.NewInt(int64(m.code[m.pc])))

		case PUSH32:
			if m.pc+32 >= len(m.code) {
				return ErrInvalidOpcode
			}
			word := m.code[m.pc+1 : m.pc+33]
			m.pc += 32
			m.push(new(big.Int).SetBytes(word))

		case POP:
			if _, err := m.pop(); err != nil {
				return err
			}

		case DUP:
			if len(m.stack) == 0 {
				return ErrStackUnderflow
			}
			top := m.stack[len(m.stack)-1]
			m.push(new(big.Int).Set(top))

		case SWAP:
			if len(m.stack) < 2 {
				return ErrStackUnderflow
			}
			n := len(m.stack)
			m.stack[n-1], m.stack[n-2] = m.stack[n-2], m.stack[n-1]

		case MLOAD:
			offset, err := m.pop()
			if err != nil {
				return err
			}
			off := int(offset.Uint64())
			m.growMemory(off + 32)
			word := m.memory[off : off+32]
			m.push(new(big.Int).SetBytes(word))

		case MSTORE:
			offset, val, err := m.pop2()
			if err != nil {
				return err
			}
			off := int(offset.Uint64())
			m.growMemory(off + 32)
			b := val.Bytes()
			dest := m.memory[off : off+32]
			for i := range dest {
				dest[i] = 0
			}
			copy(dest[32-len(b):], b)

		case SLOAD:
			key, err := m.pop()
			if err != nil {
				return err
			}
			kBytes := padTo32(key.Bytes())
			val := m.contract.StorageGet(kBytes)
			m.push(new(big.Int).SetBytes(val))

		case SSTORE:
			key, val, err := m.pop2()
			if err != nil {
				return err
			}
			m.contract.StorageSet(padTo32(key.Bytes()), padTo32(val.Bytes()))

		case JUMP:
			dest, err := m.pop()
			if err != nil {
				return err
			}
			d := int(dest.Uint64())
			if d < 0 || d >= len(m.code) || OpCode(m.code[d]) != JUMPDEST {
				return ErrInvalidJump
			}
			m.pc = d
			continue

		case JUMPI:
			dest, cond, err := m.pop2()
			if err != nil {
				return err
			}
			if cond.Sign() != 0 {
				d := int(dest.Uint64())
				if d < 0 || d >= len(m.code) || OpCode(m.code[d]) != JUMPDEST {
					return ErrInvalidJump
				}
				m.pc = d
				continue
			}

		case JUMPDEST:
			// marker only, no action

		case PC:
			m.push(big.NewInt(int64(m.pc)))

		case GAS:
			m.push(new(big.Int).SetUint64(m.gasLeft))

		case RETURN:
			offset, size, err := m.pop2()
			if err != nil {
				return err
			}
			off := int(offset.Uint64())
			sz := int(size.Uint64())
			m.growMemory(off + sz)
			m.returnData = make([]byte, sz)
			copy(m.returnData, m.memory[off:off+sz])
			return nil

		case REVERT:
			offset, size, err := m.pop2()
			if err != nil {
				return err
			}
			off := int(offset.Uint64())
			sz := int(size.Uint64())
			m.growMemory(off + sz)
			m.returnData = make([]byte, sz)
			copy(m.returnData, m.memory[off:off+sz])
			return ErrExecutionReverted

		case LOG0:
			offset, size, err := m.pop2()
			if err != nil {
				return err
			}
			off := int(offset.Uint64())
			sz := int(size.Uint64())
			m.growMemory(off + sz)
			data := m.memory[off : off+sz]
			m.logs = append(m.logs, Log{
				Address: m.contract.Address,
				Topics:  []string{},
				Data:    hex.EncodeToString(data),
			})

		case LOG1:
			offset, size, err := m.pop2()
			if err != nil {
				return err
			}
			topic, err2 := m.pop()
			if err2 != nil {
				return err2
			}
			off := int(offset.Uint64())
			sz := int(size.Uint64())
			m.growMemory(off + sz)
			data := m.memory[off : off+sz]
			m.logs = append(m.logs, Log{
				Address: m.contract.Address,
				Topics:  []string{hex.EncodeToString(padTo32(topic.Bytes()))},
				Data:    hex.EncodeToString(data),
			})

		case ORIGIN:
			addr := addrToWord(m.ctx.Origin)
			m.push(new(big.Int).SetBytes(addr))

		case CALLER:
			addr := addrToWord(m.ctx.Caller)
			m.push(new(big.Int).SetBytes(addr))

		case CALLVALUE:
			v := m.ctx.Value
			if v == nil {
				v = big.NewInt(0)
			}
			m.push(new(big.Int).Set(v))

		case ADDRESS:
			addr := addrToWord(m.contract.Address)
			m.push(new(big.Int).SetBytes(addr))

		case BALANCE:
			addrWord, err := m.pop()
			if err != nil {
				return err
			}
			addrStr := "0x" + hex.EncodeToString(padTo32(addrWord.Bytes())[12:])
			var bal *big.Int
			if m.ctx.GetBalance != nil {
				bal = m.ctx.GetBalance(addrStr)
			} else {
				bal = big.NewInt(0)
			}
			if bal == nil {
				bal = big.NewInt(0)
			}
			m.push(new(big.Int).Set(bal))

		case BLOCKNUMBER:
			m.push(new(big.Int).SetUint64(m.ctx.BlockNumber))

		case TIMESTAMP:
			m.push(big.NewInt(m.ctx.Timestamp))

		case SHA3:
			offset, size, err := m.pop2()
			if err != nil {
				return err
			}
			off := int(offset.Uint64())
			sz := int(size.Uint64())
			m.growMemory(off + sz)
			sum := sha256.Sum256(m.memory[off : off+sz])
			m.push(new(big.Int).SetBytes(sum[:]))

		case CALL:
			// stub: consume gas, push 0 (failure) — full cross-contract calls need re-entrancy guard
			for i := 0; i < 7; i++ {
				if _, err := m.pop(); err != nil {
					return err
				}
			}
			m.push(big.NewInt(0))

		default:
			return fmt.Errorf("%w: 0x%02x at PC=%d", ErrInvalidOpcode, op, m.pc)
		}

		m.pc++
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m *machine) push(v *big.Int) {
	m.stack = append(m.stack, v)
}

func (m *machine) pop() (*big.Int, error) {
	if len(m.stack) == 0 {
		return nil, ErrStackUnderflow
	}
	n := len(m.stack) - 1
	v := m.stack[n]
	m.stack = m.stack[:n]
	return v, nil
}

func (m *machine) pop2() (*big.Int, *big.Int, error) {
	b, err := m.pop()
	if err != nil {
		return nil, nil, err
	}
	a, err := m.pop()
	if err != nil {
		return nil, nil, err
	}
	return a, b, nil
}

func (m *machine) growMemory(size int) {
	if size > MaxMemory {
		size = MaxMemory
	}
	for len(m.memory) < size {
		m.memory = append(m.memory, 0)
	}
}

func padTo32(b []byte) []byte {
	if len(b) >= 32 {
		return b[:32]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func addrToWord(addr string) []byte {
	addr = addr[2:] // strip 0x
	b, _ := hex.DecodeString(addr)
	return padTo32(b)
}

// ── ABI helpers (minimal) ──────────────────────────────────────────────────────

// EncodeUint256 ABI-encodes a uint256.
func EncodeUint256(n *big.Int) []byte {
	return padTo32(n.Bytes())
}

// EncodeSelector returns the 4-byte function selector for a signature like "transfer(address,uint256)".
func EncodeSelector(sig string) []byte {
	sum := sha256.Sum256([]byte(sig))
	return sum[:4]
}

// DecodeUint256 ABI-decodes a uint256 from a 32-byte word.
func DecodeUint256(b []byte) *big.Int {
	if len(b) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(b):], b)
		b = padded
	}
	return new(big.Int).SetBytes(b[:32])
}

// EncodeCalldata builds minimal ABI calldata: selector + args.
func EncodeCalldata(selector []byte, args ...*big.Int) []byte {
	data := make([]byte, 4+32*len(args))
	copy(data, selector[:4])
	for i, arg := range args {
		encoded := padTo32(arg.Bytes())
		copy(data[4+i*32:], encoded)
	}
	return data
}

// ─── Serialisation ─────────────────────────────────────────────────────────────

// MarshalContract serialises a ContractAccount to JSON (for storage).
func MarshalContract(ca *ContractAccount) ([]byte, error) {
	type wire struct {
		Address  string            `json:"address"`
		Code     string            `json:"code"`
		Storage  map[string]string `json:"storage"`
		Balance  string            `json:"balance"`
		CodeHash string            `json:"codeHash"`
		Nonce    uint64            `json:"nonce"`
	}
	bal := "0"
	if ca.Balance != nil {
		bal = ca.Balance.String()
	}
	return json.Marshal(wire{
		Address:  ca.Address,
		Code:     hex.EncodeToString(ca.Code),
		Storage:  ca.Storage,
		Balance:  bal,
		CodeHash: ca.CodeHash,
		Nonce:    ca.Nonce,
	})
}

// UnmarshalContract deserialises a ContractAccount from JSON.
func UnmarshalContract(data []byte) (*ContractAccount, error) {
	type wire struct {
		Address  string            `json:"address"`
		Code     string            `json:"code"`
		Storage  map[string]string `json:"storage"`
		Balance  string            `json:"balance"`
		CodeHash string            `json:"codeHash"`
		Nonce    uint64            `json:"nonce"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	code, _ := hex.DecodeString(w.Code)
	bal := new(big.Int)
	bal.SetString(w.Balance, 10)
	return &ContractAccount{
		Address:  w.Address,
		Code:     code,
		Storage:  w.Storage,
		Balance:  bal,
		CodeHash: w.CodeHash,
		Nonce:    w.Nonce,
	}, nil
}

// Uint64ToBytes converts a uint64 to big-endian 8 bytes.
func Uint64ToBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}
