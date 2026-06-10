package rpc

import (
        "context"
        "crypto/sha256"
        "encoding/hex"
        "encoding/json"
        "fmt"
	"io/fs"
        "math/big"
        "net/http"
        "strconv"
        "strings"
        "sync"
        "time"

        "github.com/gorilla/mux"
        "github.com/gorilla/websocket"
        "github.com/rs/zerolog/log"

        "github.com/gydschain/fullnode/core"
)

type Server struct {
        chain      *core.Chain
        router     *mux.Router
        httpServer *http.Server
        upgrader   websocket.Upgrader
        subs       map[string]*subscriber
        subsMu     sync.RWMutex
        port       int

        pendingTx   map[string]*core.Transaction
        pendingTxMu sync.RWMutex
}

type subscriber struct {
        conn *websocket.Conn
        ch   chan interface{}
}

func NewServer(chain *core.Chain, port int) *Server {
        s := &Server{
                chain: chain,
                port:  port,
                upgrader: websocket.Upgrader{
                        CheckOrigin: func(r *http.Request) bool { return true },
                },
                subs:      make(map[string]*subscriber),
                pendingTx: make(map[string]*core.Transaction),
        }
        s.setupRoutes()
        return s
}

func cors(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
                w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
                if r.Method == http.MethodOptions {
                        w.WriteHeader(http.StatusNoContent)
                        return
                }
                next.ServeHTTP(w, r)
        })
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}

func (s *Server) setupRoutes() {
        r := mux.NewRouter()

        r.HandleFunc("/health", s.handleHealth).Methods("GET")
	r.HandleFunc("/", s.handleDashboard).Methods("GET")
        r.HandleFunc("/", s.handleJSONRPC).Methods("POST", "OPTIONS")
        r.HandleFunc("/rpc", s.handleJSONRPC).Methods("POST", "OPTIONS")

        api := r.PathPrefix("/api").Subrouter()
        api.HandleFunc("/status", s.handleStatus).Methods("GET")
        api.HandleFunc("/blocks", s.handleBlocks).Methods("GET")
        api.HandleFunc("/blocks/{id}", s.handleBlock).Methods("GET")
        api.HandleFunc("/transactions", s.handleTransactions).Methods("GET")
        api.HandleFunc("/peers", s.handlePeers).Methods("GET")
        api.HandleFunc("/ws", s.handleWS)

        r.Use(cors)
        s.router = r
}

func (s *Server) Start() error {
        s.httpServer = &http.Server{
                Addr:         fmt.Sprintf(":%d", s.port),
                Handler:      s.router,
                ReadTimeout:  15 * time.Second,
                WriteTimeout: 15 * time.Second,
        }
        log.Info().Int("port", s.port).Msg("RPC server listening")
        return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
        return s.httpServer.Shutdown(ctx)
}

func (s *Server) NotifyNewBlock(b *core.Block) {
        msg := map[string]interface{}{
                "type": "newBlock",
                "data": b.ToMap(),
        }
        s.broadcast(msg)
}

func (s *Server) broadcast(msg interface{}) {
        s.subsMu.RLock()
        defer s.subsMu.RUnlock()
        for _, sub := range s.subs {
                select {
                case sub.ch <- msg:
                default:
                }
        }
}

func jsonOK(w http.ResponseWriter, v interface{}) {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(code)
        json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
        jsonOK(w, map[string]interface{}{"status": "ok", "height": s.chain.Height()})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
        jsonOK(w, s.chain.Stats())
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
        limitStr := r.URL.Query().Get("limit")
        limit := 20
        if limitStr != "" {
                if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
                        if n > 50 {
                                n = 50
                        }
                        limit = n
                }
        }
        blocks := s.chain.LatestBlocks(limit)
        out := make([]map[string]interface{}, len(blocks))
        for i, b := range blocks {
                out[i] = b.ToMap()
        }
        jsonOK(w, map[string]interface{}{"blocks": out, "count": len(out)})
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
        id := mux.Vars(r)["id"]
        var block *core.Block
        var err error
        if num, e := strconv.ParseUint(id, 10, 64); e == nil {
                block, err = s.chain.GetByNumber(num)
        } else {
                block, err = s.chain.GetByHash(id)
        }
        if err != nil {
                jsonErr(w, http.StatusNotFound, "block not found")
                return
        }
        jsonOK(w, map[string]interface{}{"block": block.ToMap()})
}

func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
        limitStr := r.URL.Query().Get("limit")
        limit := 20
        if limitStr != "" {
                if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
                        if n > 50 {
                                n = 50
                        }
                        limit = n
                }
        }
        blocks := s.chain.LatestBlocks(limit)
        var txs []map[string]interface{}
        for _, b := range blocks {
                for _, tx := range b.Transactions {
                        txs = append(txs, tx.ToMap())
                        if len(txs) >= limit {
                                break
                        }
                }
                if len(txs) >= limit {
                        break
                }
        }
        jsonOK(w, map[string]interface{}{"transactions": txs, "count": len(txs)})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
        jsonOK(w, map[string]interface{}{"peers": []interface{}{}, "count": 0})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
        conn, err := s.upgrader.Upgrade(w, r, nil)
        if err != nil {
                return
        }
        id := fmt.Sprintf("%p", conn)
        sub := &subscriber{conn: conn, ch: make(chan interface{}, 32)}
        s.subsMu.Lock()
        s.subs[id] = sub
        s.subsMu.Unlock()
        defer func() {
                s.subsMu.Lock()
                delete(s.subs, id)
                s.subsMu.Unlock()
                conn.Close()
        }()
        go func() {
                for msg := range sub.ch {
                        conn.WriteJSON(msg)
                }
        }()
        for {
                if _, _, err := conn.ReadMessage(); err != nil {
                        return
                }
        }
}

// ── JSON-RPC ─────────────────────────────────────────────────────────────────

type jsonRPCRequest struct {
        JSONRPC string        `json:"jsonrpc"`
        Method  string        `json:"method"`
        Params  []interface{} `json:"params"`
        ID      interface{}   `json:"id"`
}

type jsonRPCResponse struct {
        JSONRPC string      `json:"jsonrpc"`
        Result  interface{} `json:"result,omitempty"`
        Error   interface{} `json:"error,omitempty"`
        ID      interface{} `json:"id"`
}

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
        body := r.Body
        defer body.Close()

        // Support both single request and batch (array)
        var raw json.RawMessage
        if err := json.NewDecoder(body).Decode(&raw); err != nil {
                jsonErr(w, http.StatusBadRequest, "invalid JSON")
                return
        }

        w.Header().Set("Content-Type", "application/json")

        if len(raw) > 0 && raw[0] == '[' {
                var reqs []jsonRPCRequest
                if err := json.Unmarshal(raw, &reqs); err != nil {
                        jsonErr(w, http.StatusBadRequest, "invalid batch JSON")
                        return
                }
                responses := make([]jsonRPCResponse, len(reqs))
                for i, req := range reqs {
                        responses[i] = s.dispatch(req)
                }
                json.NewEncoder(w).Encode(responses)
                return
        }

        var req jsonRPCRequest
        if err := json.Unmarshal(raw, &req); err != nil {
                jsonErr(w, http.StatusBadRequest, "invalid JSON")
                return
        }
        json.NewEncoder(w).Encode(s.dispatch(req))
}

func paramStr(params []interface{}, idx int) string {
        if len(params) > idx {
                if s, ok := params[idx].(string); ok {
                        return s
                }
        }
        return ""
}

func (s *Server) dispatch(req jsonRPCRequest) jsonRPCResponse {
        resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}

        switch req.Method {

        // ── Network / chain info ─────────────────────────────────────────────────
        case "eth_blockNumber":
                resp.Result = fmt.Sprintf("0x%x", s.chain.Height())

        case "eth_chainId":
                stats := s.chain.Stats()
                resp.Result = fmt.Sprintf("0x%x", stats["chainId"])

        case "net_version":
                stats := s.chain.Stats()
                resp.Result = fmt.Sprintf("%v", stats["chainId"])

        case "net_listening":
                resp.Result = true

        case "net_peerCount":
                resp.Result = "0x0"

        case "eth_syncing":
                resp.Result = false

        case "web3_clientVersion":
                resp.Result = "GYDS/v1.0.0/linux/go1.25"

        case "eth_protocolVersion":
                resp.Result = "0x41"

        // ── Gas ──────────────────────────────────────────────────────────────────
        case "eth_gasPrice":
                resp.Result = "0x3B9ACA00" // 1 gwei

        case "eth_maxPriorityFeePerGas":
                resp.Result = "0x3B9ACA00"

        case "eth_feeHistory":
                resp.Result = map[string]interface{}{
                        "baseFeePerGas": []string{"0x3B9ACA00"},
                        "gasUsedRatio":  []float64{0.5},
                        "oldestBlock":   fmt.Sprintf("0x%x", s.chain.Height()),
                        "reward":        [][]string{{"0x0"}},
                }

        case "eth_estimateGas":
                resp.Result = "0x5208" // 21000

        // ── Blocks ───────────────────────────────────────────────────────────────
        case "eth_getBlockByNumber":
                numStr := paramStr(req.Params, 0)
                if numStr == "latest" || numStr == "" {
                        head := s.chain.Head()
                        if head != nil {
                                resp.Result = blockToRPC(head)
                        } else {
                                resp.Result = nil
                        }
                } else {
                        var num uint64
                        fmt.Sscanf(numStr, "0x%x", &num)
                        if b, err := s.chain.GetByNumber(num); err == nil {
                                resp.Result = blockToRPC(b)
                        } else {
                                resp.Result = nil
                        }
                }

        case "eth_getBlockByHash":
                hashStr := paramStr(req.Params, 0)
                if b, err := s.chain.GetByHash(hashStr); err == nil {
                        resp.Result = blockToRPC(b)
                } else {
                        resp.Result = nil
                }

        case "eth_getBlockTransactionCountByNumber":
                numStr := paramStr(req.Params, 0)
                var num uint64
                if numStr == "latest" {
                        num = s.chain.Height()
                } else {
                        fmt.Sscanf(numStr, "0x%x", &num)
                }
                if b, err := s.chain.GetByNumber(num); err == nil {
                        resp.Result = fmt.Sprintf("0x%x", len(b.Transactions))
                } else {
                        resp.Result = "0x0"
                }

        case "eth_getBlockTransactionCountByHash":
                hashStr := paramStr(req.Params, 0)
                if b, err := s.chain.GetByHash(hashStr); err == nil {
                        resp.Result = fmt.Sprintf("0x%x", len(b.Transactions))
                } else {
                        resp.Result = "0x0"
                }

        // ── Accounts ─────────────────────────────────────────────────────────────
        case "eth_accounts":
                resp.Result = []string{}

        case "eth_getBalance":
                addr := paramStr(req.Params, 0)
                bal := s.chain.GetBalance(addr)
                resp.Result = fmt.Sprintf("0x%x", bal)

        case "eth_getTransactionCount":
                addr := paramStr(req.Params, 0)
                nonce := s.chain.GetNonce(addr)
                resp.Result = fmt.Sprintf("0x%x", nonce)

        case "eth_getCode":
                resp.Result = "0x"

        case "eth_getStorageAt":
                resp.Result = "0x0000000000000000000000000000000000000000000000000000000000000000"

        // ── Transactions ─────────────────────────────────────────────────────────
        case "eth_sendRawTransaction":
                raw := paramStr(req.Params, 0)
                txHash := hashRawTx(raw)

                s.pendingTxMu.Lock()
                s.pendingTx[txHash] = &core.Transaction{
                        Hash:      txHash,
                        From:      "0x0000000000000000000000000000000000000000",
                        To:        "0x0000000000000000000000000000000000000000",
                        Value:     big.NewInt(0),
                        GasLimit:  21000,
                        GasPrice:  big.NewInt(1_000_000_000),
                        GasUsed:   21000,
                        Status:    "pending",
                        Timestamp: time.Now().Unix(),
                }
                s.pendingTxMu.Unlock()

                resp.Result = txHash

        case "eth_getTransactionByHash":
                hash := paramStr(req.Params, 0)
                if tx, ok := s.chain.GetTransaction(hash); ok {
                        resp.Result = txToRPC(tx)
                } else {
                        s.pendingTxMu.RLock()
                        pending, found := s.pendingTx[hash]
                        s.pendingTxMu.RUnlock()
                        if found {
                                resp.Result = txToRPC(pending)
                        } else {
                                resp.Result = nil
                        }
                }

        case "eth_getTransactionReceipt":
                hash := paramStr(req.Params, 0)
                if tx, ok := s.chain.GetTransaction(hash); ok {
                        resp.Result = txReceiptRPC(tx, s.chain)
                } else {
                        s.pendingTxMu.RLock()
                        _, found := s.pendingTx[hash]
                        s.pendingTxMu.RUnlock()
                        if found {
                                // Pending — no receipt yet
                                resp.Result = nil
                        } else {
                                resp.Result = nil
                        }
                }

        // ── Calls ────────────────────────────────────────────────────────────────
        case "eth_call":
                resp.Result = "0x"

        case "eth_getLogs":
                resp.Result = []interface{}{}

        case "eth_newFilter", "eth_newBlockFilter", "eth_newPendingTransactionFilter":
                resp.Result = "0x1"

        case "eth_getFilterChanges", "eth_getFilterLogs":
                resp.Result = []interface{}{}

        case "eth_uninstallFilter":
                resp.Result = true

        default:
                resp.Error = map[string]interface{}{
                        "code":    -32601,
                        "message": fmt.Sprintf("method %s not found", req.Method),
                }
        }

        return resp
}

// ── RPC formatters ────────────────────────────────────────────────────────────

func blockToRPC(b *core.Block) map[string]interface{} {
        txHashes := make([]string, len(b.Transactions))
        for i, tx := range b.Transactions {
                txHashes[i] = tx.Hash
        }
        return map[string]interface{}{
                "number":           fmt.Sprintf("0x%x", b.Header.Number),
                "hash":             b.Hash,
                "parentHash":       b.Header.ParentHash,
                "stateRoot":        b.Header.StateRoot,
                "transactionsRoot": b.Header.TxRoot,
                "receiptsRoot":     b.Header.ReceiptRoot,
                "miner":            b.Header.Validator,
                "difficulty":       "0x1",
                "totalDifficulty":  fmt.Sprintf("0x%x", b.Header.Number),
                "size":             fmt.Sprintf("0x%x", b.Header.Size),
                "gasLimit":         fmt.Sprintf("0x%x", b.Header.GasLimit),
                "gasUsed":          fmt.Sprintf("0x%x", b.Header.GasUsed),
                "timestamp":        fmt.Sprintf("0x%x", b.Header.Timestamp),
                "transactions":     txHashes,
                "uncles":           []string{},
                "baseFeePerGas":    "0x3B9ACA00",
                "extraData":        "0x",
                "logsBloom":        "0x" + strings.Repeat("0", 512),
                "mixHash":          "0x" + strings.Repeat("0", 64),
                "nonce":            "0x0000000000000000",
                "sha3Uncles":       "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
        }
}

func txToRPC(tx *core.Transaction) map[string]interface{} {
        value := "0x0"
        if tx.Value != nil {
                value = fmt.Sprintf("0x%x", tx.Value)
        }
        gasPrice := "0x3B9ACA00"
        if tx.GasPrice != nil {
                gasPrice = fmt.Sprintf("0x%x", tx.GasPrice)
        }
        return map[string]interface{}{
                "hash":             tx.Hash,
                "from":             tx.From,
                "to":               tx.To,
                "value":            value,
                "gas":              fmt.Sprintf("0x%x", tx.GasLimit),
                "gasPrice":         gasPrice,
                "nonce":            fmt.Sprintf("0x%x", tx.Nonce),
                "input":            "0x",
                "blockHash":        nil,
                "blockNumber":      nil,
                "transactionIndex": "0x0",
                "type":             "0x0",
                "v":                "0x1",
                "r":                "0x" + strings.Repeat("0", 64),
                "s":                "0x" + strings.Repeat("0", 64),
        }
}

func txReceiptRPC(tx *core.Transaction, chain *core.Chain) map[string]interface{} {
        blockNum := "0x0"
        blockHash := "0x" + strings.Repeat("0", 64)
        if b, err := chain.GetByNumber(tx.BlockNum); err == nil {
                blockNum = fmt.Sprintf("0x%x", b.Header.Number)
                blockHash = b.Hash
        }
        return map[string]interface{}{
                "transactionHash":   tx.Hash,
                "transactionIndex":  "0x0",
                "blockHash":         blockHash,
                "blockNumber":       blockNum,
                "from":              tx.From,
                "to":                tx.To,
                "gasUsed":           fmt.Sprintf("0x%x", tx.GasUsed),
                "cumulativeGasUsed": fmt.Sprintf("0x%x", tx.GasUsed),
                "contractAddress":   nil,
                "logs":              []interface{}{},
                "logsBloom":         "0x" + strings.Repeat("0", 512),
                "status":            "0x1",
                "type":              "0x0",
                "effectiveGasPrice": "0x3B9ACA00",
        }
}

// hashRawTx creates a deterministic tx hash from raw hex bytes.
func hashRawTx(raw string) string {
        raw = strings.TrimPrefix(raw, "0x")
        bytes, _ := hex.DecodeString(raw)
        if len(bytes) == 0 {
                bytes = []byte(raw)
        }
        sum := sha256.Sum256(bytes)
        return "0x" + hex.EncodeToString(sum[:])
}
