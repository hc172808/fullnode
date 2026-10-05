package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
)

const rpcTestRecipient = "0x0000000000000000000000000000000000000100"

// Use the actual RPC router and middleware, without starting the dashboard,
// update checker, or touching operator data.
func compatibilityServer(t *testing.T, genesis *core.GenesisConfig) *Server {
	t.Helper()
	chain := core.NewChain(genesis, t.TempDir())
	t.Cleanup(func() { chain.Close() })
	s := &Server{
		chain:    chain,
		subs:     make(map[string]*subscriber),
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
	s.setupRPCRoutes()
	return s
}

func rpcRequest(t *testing.T, s *Server, method string, params ...interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params, ID: "compatibility"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.rpcRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%s: status=%d headers=%v body=%s", method, w.Code, w.Header(), w.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["jsonrpc"] != "2.0" || response["id"] != "compatibility" {
		t.Fatalf("invalid response envelope: %#v", response)
	}
	_, result := response["result"]
	_, rpcErr := response["error"]
	if result == rpcErr {
		t.Fatalf("%s: expected exactly one of result/error: %#v", method, response)
	}
	return response
}

func rpcResult(t *testing.T, s *Server, method string, params ...interface{}) interface{} {
	t.Helper()
	response := rpcRequest(t, s, method, params...)
	if err, ok := response["error"]; ok {
		t.Fatalf("%s failed: %#v", method, err)
	}
	return response["result"]
}

func requireRPCError(t *testing.T, response map[string]interface{}, code int) {
	t.Helper()
	err, ok := response["error"].(map[string]interface{})
	if !ok || err["code"] != float64(code) || err["message"] == "" {
		t.Fatalf("want error %d with message, got %#v", code, response)
	}
}

func TestRPCNetEnodeRequiresAdvertisedHostAndReturnsStableEndpoint(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	requireRPCError(t, rpcRequest(t, s, "net_enode"), -32001)

	key, err := p2p.LoadOrCreateNodeKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p2pServer := p2p.NewServer(30303, 198282, nil)
	p2pServer.SetAuth(key, false, nil)
	p2pServer.SetAdvertiseHost("node.example.net")
	s.SetP2P(p2pServer)

	want := "enode://" + key.ID() + "@node.example.net:30303"
	for range 2 {
		if got := rpcResult(t, s, "net_enode"); got != want {
			t.Fatalf("net_enode = %#v, want %q", got, want)
		}
	}
}

func TestRPCNetworkAndBlockReads(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	for method, want := range map[string]interface{}{
		"eth_chainId": "0x3068a", "net_version": "198282",
		"eth_syncing": false, "eth_blockNumber": "0x0", "net_peerCount": "0x0",
	} {
		t.Run(method, func(t *testing.T) {
			if got := rpcResult(t, s, method); got != want {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
	for _, tag := range []string{"latest", "0x0"} {
		block := rpcResult(t, s, "eth_getBlockByNumber", tag, false).(map[string]interface{})
		if block["number"] != "0x0" || block["hash"] != s.chain.Head().Hash {
			t.Fatalf("unexpected block: %#v", block)
		}
		byHash := rpcResult(t, s, "eth_getBlockByHash", block["hash"], false).(map[string]interface{})
		if byHash["hash"] != block["hash"] {
			t.Fatalf("hash and number lookups disagree: %#v", byHash)
		}
	}
	for _, method := range []string{"eth_getBlockByNumber", "eth_getBlockByHash", "eth_getTransactionByHash", "eth_getTransactionReceipt"} {
		if got := rpcResult(t, s, method, "0x"+strings.Repeat("f", 64)); got != nil {
			t.Fatalf("%s unknown lookup = %#v, want null", method, got)
		}
	}
	if got := rpcResult(t, s, "eth_getBalance", core.GydsGenesis.Alloc[0].Address, "latest"); got != hexutil.EncodeBig(core.GydsGenesis.Alloc[0].Balance) {
		t.Fatalf("genesis balance = %v", got)
	}
	if got := rpcResult(t, s, "eth_getBalance", rpcTestRecipient, "latest"); got != "0x0" {
		t.Fatalf("unknown account balance = %v", got)
	}
}

func TestRPCExecutionDisabledAndInvalidInputs(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	for _, method := range []string{"eth_call", "eth_estimateGas"} {
		requireRPCError(t, rpcRequest(t, s, method, map[string]interface{}{"to": rpcTestRecipient}, "latest"), -32000)
		for _, params := range [][]interface{}{
			nil, {"not an object"},
			{map[string]interface{}{"data": "0xzz"}},
			{map[string]interface{}{"value": "1"}},
			{map[string]interface{}{"gas": "0xzz"}},
		} {
			requireRPCError(t, rpcRequest(t, s, method, params...), -32602)
		}
	}
	requireRPCError(t, rpcRequest(t, s, "eth_sendRawTransaction"), -32602)
	requireRPCError(t, rpcRequest(t, s, "eth_sendRawTransaction", "0xzz"), -32602)
	requireRPCError(t, rpcRequest(t, s, "eth_sendRawTransaction", "0xdeadbeef"), -32000)
	requireRPCError(t, rpcRequest(t, s, "eth_subscribe", "newHeads"), -32601)
}

func TestRPCSignedTransactionLifecycle(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := crypto.PubkeyToAddress(key.PublicKey).Hex()
	genesis := &core.GenesisConfig{
		ChainID: 31337, NetworkName: "RPC compatibility test",
		EVMActivationBlock: 1, GasLimit: 30_000_000, Difficulty: big.NewInt(1),
		Validators: []string{sender},
		Alloc:      []core.GenesisAlloc{{Address: sender, Balance: new(big.Int).Mul(big.NewInt(5), big.NewInt(1e18))}},
	}
	s := compatibilityServer(t, genesis)
	if got := rpcResult(t, s, "eth_chainId"); got != "0x7a69" {
		t.Fatalf("test chain ID = %v", got)
	}
	args := map[string]interface{}{"from": sender, "to": rpcTestRecipient, "value": "0x7"}
	if got := rpcResult(t, s, "eth_call", args, "latest"); got != "0x" {
		t.Fatalf("call = %#v, want empty return data", got)
	}
	if got := rpcResult(t, s, "eth_estimateGas", args); got != "0x5208" {
		t.Fatalf("transfer estimate = %v, want 21000", got)
	}
	if got := rpcResult(t, s, "eth_getBalance", rpcTestRecipient, "latest"); got != "0x0" {
		t.Fatalf("call/estimate mutated state: %v", got)
	}
	to := common.HexToAddress(rpcTestRecipient)
	makeRaw := func(chainID int64) (*types.Transaction, string) {
		t.Helper()
		tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
			ChainID: big.NewInt(chainID), Nonce: 0,
			GasTipCap: big.NewInt(1e9), GasFeeCap: big.NewInt(3e9),
			Gas: 21000, To: &to, Value: big.NewInt(7),
		}), types.LatestSignerForChainID(big.NewInt(chainID)), key)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := tx.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		return tx, hexutil.Encode(raw)
	}
	_, wrongChain := makeRaw(198282)
	requireRPCError(t, rpcRequest(t, s, "eth_sendRawTransaction", wrongChain), -32000)
	signed, raw := makeRaw(31337)
	hash := rpcResult(t, s, "eth_sendRawTransaction", raw)
	if hash != signed.Hash().Hex() {
		t.Fatalf("submitted hash = %v, want %s", hash, signed.Hash().Hex())
	}
	if got := rpcResult(t, s, "eth_getTransactionCount", sender, "pending"); got != "0x1" {
		t.Fatalf("pending nonce = %v", got)
	}
	if got := rpcResult(t, s, "eth_getTransactionCount", sender, "latest"); got != "0x0" {
		t.Fatalf("confirmed nonce before inclusion = %v", got)
	}
	if got := rpcResult(t, s, "eth_getTransactionReceipt", hash); got != nil {
		t.Fatalf("pending receipt = %#v, want null", got)
	}
	pending := rpcResult(t, s, "eth_getTransactionByHash", hash).(map[string]interface{})
	if pending["hash"] != hash || pending["blockHash"] != nil || pending["blockNumber"] != nil {
		t.Fatalf("invalid pending transaction: %#v", pending)
	}
	block := core.NewBlock(s.chain.Head().Header, sender, s.chain.PendingTransactions(10))
	if err := s.chain.InsertBlock(block); err != nil {
		t.Fatal(err)
	}
	receipt := rpcResult(t, s, "eth_getTransactionReceipt", hash).(map[string]interface{})
	for field, want := range map[string]interface{}{
		"transactionHash": hash, "blockHash": block.Hash, "blockNumber": "0x1",
		"status": "0x1", "gasUsed": "0x5208", "from": sender, "to": to.Hex(),
	} {
		if receipt[field] != want {
			t.Fatalf("receipt %s = %#v, want %#v", field, receipt[field], want)
		}
	}
	if got := rpcResult(t, s, "eth_getBalance", rpcTestRecipient, "latest"); got != "0x7" {
		t.Fatalf("confirmed recipient balance = %v", got)
	}
	if got := rpcResult(t, s, "eth_getTransactionCount", sender, "latest"); got != "0x1" {
		t.Fatalf("confirmed nonce = %v", got)
	}
	requireRPCError(t, rpcRequest(t, s, "eth_sendRawTransaction", raw), -32000)
}

func TestRPCBatchAndCORS(t *testing.T) {
	s := compatibilityServer(t, core.GydsGenesis)
	w := httptest.NewRecorder()
	s.rpcRouter.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(
		`[{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1},{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["0xdead"],"id":2},{"jsonrpc":"2.0","method":"missing_method","params":[],"id":3}]`)))
	var responses []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &responses); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(responses) != 3 {
		t.Fatalf("invalid batch response: %s", w.Body.String())
	}
	for i, response := range responses {
		if response["id"] != float64(i+1) || response["jsonrpc"] != "2.0" {
			t.Fatalf("batch response ID/version: %#v", response)
		}
	}
	if responses[0]["result"] != "0x3068a" {
		t.Fatal(responses[0])
	}
	if result, exists := responses[1]["result"]; !exists || result != nil {
		t.Fatalf("missing explicit null result: %#v", responses[1])
	}
	requireRPCError(t, responses[2], -32601)
	w = httptest.NewRecorder()
	s.rpcRouter.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/", nil))
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wallet CORS preflight: status=%d headers=%v", w.Code, w.Header())
	}
}
