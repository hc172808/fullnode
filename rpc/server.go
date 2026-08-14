package rpc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/gydschain/fullnode/core"
	"github.com/gydschain/fullnode/p2p"
)

// P2PConnector is the minimal interface the RPC server needs from the P2P layer.
type P2PConnector interface {
	ConnectTo(addr string) error
	PeerCount() int
	NodeID() string
	Enode() string
	P2PPort() int
	Peers() []p2p.PeerStatus
}

type Server struct {
	chain    *core.Chain
	upgrader websocket.Upgrader
	subs     map[string]*subscriber
	subsMu   sync.RWMutex

	dashPort   int
	dashRouter *mux.Router
	dashServer *http.Server

	rpcPort   int
	rpcRouter *mux.Router
	rpcServer *http.Server

	externalURL string
	bindHost    string // host to bind listeners on ("" / "0.0.0.0" = all interfaces, "127.0.0.1" = loopback only)
	nodeMode    string

	pendingTx   map[string]*core.Transaction
	pendingTxMu sync.RWMutex

	auth    *AuthStore
	adminDB *AdminDB
	p2p     P2PConnector
	updates *UpdateChecker

	accessLogFile *os.File
}

type subscriber struct {
	conn *websocket.Conn
	ch   chan interface{}
}

func NewServer(chain *core.Chain, dashPort, rpcPort, blockTimeSecs int, dataDir, externalURL, nodeVersion string) *Server {
	auth := NewAuthStore(dataDir)
	// A PIN supplied by the setup wizard in .env bootstraps the hash on the
	// first start. Existing hashes are never overwritten by environment data.
	// Operators can then remove the plaintext value from .env if preferred.
	if rawPin := strings.TrimSpace(os.Getenv("GYDS_DASHBOARD_PIN")); rawPin != "" && !auth.PinIsSet() {
		if err := auth.SetPin(rawPin); err != nil {
			log.Warn().Err(err).Msg("Could not initialize dashboard PIN from GYDS_DASHBOARD_PIN")
		}
	}
	adminDB, _ := NewAdminDB(dataDir)
	updater := NewUpdateChecker(nodeVersion)
	updater.Start(24 * time.Hour)

	// Open HTTP access log for fail2ban (Combined Log Format).
	// Written to <dataDir>/access.log; silently skipped if unavailable.
	var accessLogFile *os.File
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o755); err == nil {
			f, err := os.OpenFile(
				filepath.Join(dataDir, "access.log"),
				os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644,
			)
			if err == nil {
				accessLogFile = f
			}
		}
	}

	s := &Server{
		chain:       chain,
		dashPort:    dashPort,
		rpcPort:     rpcPort,
		externalURL: externalURL,
		nodeMode:    "full",
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		subs:          make(map[string]*subscriber),
		pendingTx:     make(map[string]*core.Transaction),
		auth:          auth,
		adminDB:       adminDB,
		updates:       updater,
		accessLogFile: accessLogFile,
	}
	s.setupDashboardRoutes()
	s.setupRPCRoutes()
	return s
}

// SetP2P wires the P2P server so the RPC layer can connect to imported nodes.
func (s *Server) SetP2P(p P2PConnector) { s.p2p = p }

// SetNodeMode exposes the configured local node role to the dashboard and API.
func (s *Server) SetNodeMode(mode string) {
	if mode == "" {
		mode = "full"
	}
	s.nodeMode = mode
}

// SetLoopbackOnly restricts all HTTP listeners to 127.0.0.1.
// Must be called before StartDashboard / StartRPC.
// Use this for testnode mode to guarantee no external exposure.
func (s *Server) SetLoopbackOnly() { s.bindHost = "127.0.0.1" }

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

// ── HTTP access logging (Combined Log Format — compatible with fail2ban) ──────

// loggingResponseWriter wraps http.ResponseWriter to capture status + bytes.
type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

// Hijack preserves WebSocket upgrades when the access-log middleware wraps
// the response writer. Without this delegation gorilla/websocket returns a
// 500 because it cannot take ownership of the underlying connection.
func (lw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := lw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying response writer does not support hijacking")
	}
	return hj.Hijack()
}

func (lw *loggingResponseWriter) Flush() {
	if flusher, ok := lw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (lw *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := lw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (lw *loggingResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if readerFrom, ok := lw.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(src)
		lw.bytes += int(n)
		return n, err
	}
	n, err := io.Copy(lw.ResponseWriter, src)
	lw.bytes += int(n)
	return n, err
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.status = code
	lw.ResponseWriter.WriteHeader(code)
}

func (lw *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := lw.ResponseWriter.Write(b)
	lw.bytes += n
	return n, err
}

// accessLog returns a middleware that writes one Combined Log Format line per
// request to s.accessLogFile (opened in NewServer from <dataDir>/access.log).
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		if s.accessLogFile == nil {
			return
		}
		// Combined Log Format: IP - - [timestamp] "METHOD URI PROTO" STATUS BYTES
		ip := r.RemoteAddr
		if i := strings.LastIndex(ip, ":"); i > 0 {
			ip = ip[:i]
		}
		ts := time.Now().UTC().Format("02/Jan/2006:15:04:05 +0000")
		proto := r.Proto
		if proto == "" {
			proto = "HTTP/1.1"
		}
		fmt.Fprintf(s.accessLogFile, "%s - - [%s] \"%s %s %s\" %d %d\n",
			ip, ts, r.Method, r.URL.RequestURI(), proto, lw.status, lw.bytes)
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

// setupDashboardRoutes builds the router for the web dashboard (UI + REST API).
// It also handles JSON-RPC at POST /rpc so the built-in wallet works same-origin.
func (s *Server) setupDashboardRoutes() {
	r := mux.NewRouter()

	r.HandleFunc("/health", s.handleHealth).Methods("GET")
	r.HandleFunc("/", s.handleDashboard).Methods("GET")

	// JSON-RPC at /rpc on the dashboard port — used by the built-in browser wallet
	r.HandleFunc("/rpc", s.handleJSONRPC).Methods("POST", "OPTIONS")

	// Static assets (JS, CSS, images)
	r.HandleFunc("/logo.png", s.handleLogo).Methods("GET")
	r.HandleFunc("/favicon.ico", s.handleLogo).Methods("GET")
	r.HandleFunc("/{file:.*\\.js}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.css}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.jpg}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.jpeg}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.png}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.gif}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.ico}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.webp}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.svg}", s.handleStaticAsset).Methods("GET")

	// Connection info download
	r.HandleFunc("/gyds-connection-info.json", s.handleConnectionInfo).Methods("GET")
	r.HandleFunc("/gyds-network.json", s.handleNetworkMetadata).Methods("GET")
	r.HandleFunc("/gyd-token.json", s.handleGYDMetadata).Methods("GET")

	api := r.PathPrefix("/api").Subrouter()
	api.HandleFunc("/status", s.handleStatus).Methods("GET")
	api.HandleFunc("/blocks", s.handleBlocks).Methods("GET")
	api.HandleFunc("/blocks/{id}", s.handleBlock).Methods("GET")
	api.HandleFunc("/transactions", s.handleTransactions).Methods("GET")
	api.HandleFunc("/peers", s.handlePeers).Methods("GET")
	api.HandleFunc("/ws", s.handleWS)
	api.HandleFunc("/node-info", s.handleNodeInfo).Methods("GET")
	api.HandleFunc("/setup/status", s.handleSetupStatus).Methods("GET")
	api.HandleFunc("/setup/apply", s.handleSetupApply).Methods("POST")
	api.HandleFunc("/nodes/import", s.handleNodesImport).Methods("POST", "OPTIONS")
	api.HandleFunc("/token-info", s.handleTokenInfo).Methods("GET")
	api.HandleFunc("/tokens/{address}", s.handleTokenBalances).Methods("GET")
	api.HandleFunc("/node-id", s.handleNodeIDInfo).Methods("GET")
	api.HandleFunc("/lock/status", s.handleLockStatus).Methods("GET")
	api.HandleFunc("/lock/set", s.handleLockSet).Methods("POST", "OPTIONS")
	api.HandleFunc("/lock/verify", s.handleLockVerify).Methods("POST", "OPTIONS")
	api.HandleFunc("/updates", s.handleUpdates).Methods("GET")

	admin := r.PathPrefix("/admin").Subrouter()
	admin.HandleFunc("/login", s.handleAdminLoginPage).Methods("GET")
	admin.HandleFunc("/login", s.handleAdminLoginSubmit).Methods("POST")
	admin.HandleFunc("/logout", s.handleAdminLogout).Methods("GET")
	admin.HandleFunc("/set-pin", s.handleAdminSetPinPage).Methods("GET")
	admin.HandleFunc("/set-pin", s.handleAdminSetPinSubmit).Methods("POST")
	admin.HandleFunc("/wallet", s.handleAdminWallet).Methods("GET")
	admin.HandleFunc("/db", s.handleAdminDBPage).Methods("GET")
	admin.HandleFunc("/db/tables", s.requireAdminSession(s.handleDBTables)).Methods("GET")
	admin.HandleFunc("/db/tables", s.requireAdminSession(s.handleDBCreateTable)).Methods("POST")
	admin.HandleFunc("/db/tables/{table}", s.requireAdminSession(s.handleDBDropTable)).Methods("DELETE")
	admin.HandleFunc("/db/tables/{table}/records", s.requireAdminSession(s.handleDBRecords)).Methods("GET")
	admin.HandleFunc("/db/tables/{table}/records", s.requireAdminSession(s.handleDBCreateRecord)).Methods("POST")
	admin.HandleFunc("/db/tables/{table}/records/{key}", s.requireAdminSession(s.handleDBUpdateRecord)).Methods("PUT")
	admin.HandleFunc("/db/tables/{table}/records/{key}", s.requireAdminSession(s.handleDBDeleteRecord)).Methods("DELETE")

	r.HandleFunc("/setup", s.handleSetupPage).Methods("GET")
	r.HandleFunc("/guides", s.handleGuidesPage).Methods("GET")

	r.Use(cors)
	r.Use(s.accessLog)
	s.dashRouter = r
}

// setupRPCRoutes builds the minimal router for the dedicated JSON-RPC port.
// This is what external wallets (MetaMask, etc.) connect to.
func (s *Server) setupRPCRoutes() {
	r := mux.NewRouter()
	r.HandleFunc("/health", s.handleHealth).Methods("GET")
	// The dedicated RPC origin is commonly the URL given to external wallets.
	// Serve the same public metadata and logo assets there as on the dashboard
	// so iconUrls do not resolve to 404.
	r.HandleFunc("/logo.png", s.handleLogo).Methods("GET")
	r.HandleFunc("/favicon.ico", s.handleLogo).Methods("GET")
	r.HandleFunc("/gyds-network.json", s.handleNetworkMetadata).Methods("GET")
	r.HandleFunc("/gyd-token.json", s.handleGYDMetadata).Methods("GET")
	r.HandleFunc("/{file:.*\\.jpg}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.jpeg}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.png}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.gif}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.ico}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.webp}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/{file:.*\\.svg}", s.handleStaticAsset).Methods("GET")
	r.HandleFunc("/", s.handleJSONRPC).Methods("POST", "OPTIONS")
	r.HandleFunc("/rpc", s.handleJSONRPC).Methods("POST", "OPTIONS")
	r.HandleFunc("/api/ws", s.handleWS)
	r.Use(cors)
	r.Use(s.accessLog)
	s.rpcRouter = r
}

func (s *Server) handleStaticAsset(w http.ResponseWriter, r *http.Request) {
	// Wallets commonly fetch chain icons cross-origin and cache them
	// aggressively. These headers make the public logo assets usable from
	// wallet registries without exposing any application state.
	switch strings.ToLower(filepath.Ext(r.URL.Path)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".ico":
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}

// handleLogo provides a stable, short URL for wallet registries. Some wallet
// clients reject long or changing asset paths even when the underlying image
// is served correctly.
func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	f, err := staticFiles.Open("static/gyds-coin.png")
	if err != nil {
		http.Error(w, "logo unavailable", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "logo unavailable", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleNodeInfo returns JSON with all connection endpoints for this node.
func (s *Server) handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.buildConnectionInfo())
}

// handleUpdates returns the latest auto-update status from the UpdateChecker.
func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if s.updates == nil {
		jsonOK(w, map[string]interface{}{"updateAvailable": false, "error": "update checker not initialised"})
		return
	}
	jsonOK(w, s.updates.Status())
}

// handleConnectionInfo serves gyds-connection-info.json as a downloadable file.
func (s *Server) handleConnectionInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="gyds-connection-info.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(s.buildConnectionInfo())
}

// publicBaseURL returns the configured public origin when available. When
// GYDS_EXTERNAL_URL is not set, use the incoming request so metadata remains
// useful for local/private deployments without inventing a public hostname.
func (s *Server) publicBaseURL(r *http.Request) string {
	if s.externalURL != "" {
		return strings.TrimRight(s.externalURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func websocketURL(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return base
	}
}

// handleNetworkMetadata serves wallet-readable GYDS network metadata. Wallets
// may ignore iconUrls in wallet_addEthereumChain, but this stable document is
// also useful for registries and operators that need one canonical definition.
func (s *Server) handleNetworkMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.publicBaseURL(r)
	jsonOK(w, map[string]interface{}{
		"name":       "GYDS Chain",
		"chainId":    "0x3068a",
		"chainIdHex": "0x3068a",
		"chainIdDec": 198282,
		"nativeCurrency": map[string]interface{}{
			"name":     "GYDS",
			"symbol":   "GYDS",
			"decimals": 18,
		},
		"rpcUrls":           []string{base + "/rpc"},
		"wsUrls":            []string{websocketURL(base) + "/api/ws"},
		"explorerUrls":      []string{base},
		"iconUrls":          []string{base + "/logo.png"},
		"connectionInfoUrl": base + "/gyds-connection-info.json",
	})
}

// handleGYDMetadata documents the current node-managed GYD stablecoin. It
// intentionally omits a contract address: GYD is not an ERC-20 contract yet,
// and publishing a fabricated address would cause wallets to display unsafe
// or misleading token information.
func (s *Server) handleGYDMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.publicBaseURL(r)
	jsonOK(w, map[string]interface{}{
		"name":             "GYD Stablecoin",
		"symbol":           "GYD",
		"decimals":         18,
		"totalSupply":      "10000000000",
		"isStablecoin":     true,
		"tokenType":        "node-managed-genesis-token",
		"contractAddress":  nil,
		"logoUrl":          base + "/logo.png",
		"description":      "GYD is a node-managed genesis token on GYDS Chain. It is not currently an ERC-20 contract.",
		"networkMetadata":  base + "/gyds-network.json",
		"balanceApi":       base + "/api/tokens/{address}",
		"walletImportable": false,
	})
}

func (s *Server) buildConnectionInfo() map[string]interface{} {
	stats := s.chain.Stats()
	chainID := int64(198282)
	if v, ok := stats["chainId"]; ok {
		switch cv := v.(type) {
		case int64:
			chainID = cv
		case float64:
			chainID = int64(cv)
		}
	}

	extBase := strings.TrimRight(s.externalURL, "/")
	rpcURL := fmt.Sprintf("http://0.0.0.0:%d", s.rpcPort)
	wsURL := fmt.Sprintf("ws://0.0.0.0:%d/api/ws", s.rpcPort)
	dashURL := fmt.Sprintf("http://0.0.0.0:%d", s.dashPort)
	p2pPort := 30303
	enode := ""
	peerCount := 0
	if s.p2p != nil {
		p2pPort = s.p2p.P2PPort()
		enode = s.p2p.Enode()
		peerCount = s.p2p.PeerCount()
	}
	if extBase != "" {
		// Public deployments normally terminate TLS at Nginx. Use the
		// reverse-proxy paths instead of exposing the internal listener ports.
		rpcURL = extBase + "/rpc"
		wsURL = websocketURL(extBase) + "/api/ws"
		dashURL = extBase
	}

	return map[string]interface{}{
		"network_name":  "GYDS Chain",
		"chain_id":      chainID,
		"chain_id_hex":  fmt.Sprintf("0x%x", chainID),
		"symbol":        "GYDS",
		"rpc_url":       rpcURL,
		"ws_url":        wsURL,
		"dashboard_url": dashURL,
		"p2p_port":      p2pPort,
		"enode":         enode,
		"peer_count":    peerCount,
		"ports": map[string]interface{}{
			"dashboard": s.dashPort,
			"rpc":       s.rpcPort,
			"p2p":       p2pPort,
		},
		"metamask": map[string]interface{}{
			"networkName":    "GYDS Chain",
			"rpcUrl":         rpcURL,
			"chainId":        chainID,
			"chainIdHex":     fmt.Sprintf("0x%x", chainID),
			"currencySymbol": "GYDS",
			"blockExplorer":  dashURL,
		},
		"metadata": map[string]string{
			"network": strings.TrimRight(dashURL, "/") + "/gyds-network.json",
			"gyd":     strings.TrimRight(dashURL, "/") + "/gyd-token.json",
		},
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
}

// listenAddr returns host:port for HTTP listeners.
// When bindHost is set (e.g. "127.0.0.1" for testnode), only that interface is used.
// Otherwise falls back to all interfaces (":port").
func (s *Server) listenAddr(port int) string {
	if s.bindHost != "" {
		return fmt.Sprintf("%s:%d", s.bindHost, port)
	}
	return fmt.Sprintf(":%d", port)
}

// StartDashboard starts the web dashboard on dashPort.
func (s *Server) StartDashboard() error {
	addr := s.listenAddr(s.dashPort)
	s.dashServer = &http.Server{
		Addr:         addr,
		Handler:      s.dashRouter,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
	log.Info().Str("addr", addr).Msg("Dashboard server listening")
	return s.dashServer.ListenAndServe()
}

// StartRPC starts the dedicated JSON-RPC server on rpcPort.
func (s *Server) StartRPC() error {
	addr := s.listenAddr(s.rpcPort)
	s.rpcServer = &http.Server{
		Addr:         addr,
		Handler:      s.rpcRouter,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
	log.Info().Str("addr", addr).Msg("RPC server listening")
	return s.rpcServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.dashServer != nil {
		s.dashServer.Shutdown(ctx)
	}
	if s.rpcServer != nil {
		s.rpcServer.Shutdown(ctx)
	}
	return nil
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
	stats := s.chain.Stats()
	stats["nodeMode"] = s.nodeMode
	jsonOK(w, stats)
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
	if s.p2p == nil {
		jsonOK(w, map[string]interface{}{"peers": []map[string]interface{}{}, "count": 0})
		return
	}
	peers := s.p2p.Peers()
	out := make([]map[string]interface{}, 0, len(peers))
	for _, ps := range peers {
		out = append(out, map[string]interface{}{
			"addr":       ps.Addr,
			"nodeId":     ps.NodeID,
			"height":     ps.Height,
			"nodeMode":   ps.NodeMode,
			"version":    ps.Version,
			"authorized": ps.Authorized,
		})
	}
	jsonOK(w, map[string]interface{}{"peers": out, "count": len(out)})
}

// handleNodeIDInfo returns this node's P2P identity so operators can share it.
func (s *Server) handleNodeIDInfo(w http.ResponseWriter, r *http.Request) {
	nodeID := ""
	if s.p2p != nil {
		nodeID = s.p2p.NodeID()
	}
	enode := ""
	peerCount := 0
	if s.p2p != nil {
		enode = s.p2p.Enode()
		peerCount = s.p2p.PeerCount()
	}
	jsonOK(w, map[string]interface{}{"nodeId": nodeID, "enode": enode, "peerCount": peerCount})
}

// handleTokenInfo returns metadata for every genesis-defined token.
func (s *Server) handleTokenInfo(w http.ResponseWriter, r *http.Request) {
	tokens := s.chain.TokenInfoList()
	out := make([]map[string]interface{}, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, map[string]interface{}{
			"symbol":       t.Symbol,
			"name":         t.Name,
			"decimals":     t.Decimals,
			"isStablecoin": t.IsStablecoin,
			"totalSupply":  t.TotalSupply.String(),
		})
	}
	jsonOK(w, map[string]interface{}{"tokens": out})
}

// handleTokenBalances returns all token balances for a given address.
func (s *Server) handleTokenBalances(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addr := vars["address"]
	balances := s.chain.GetAllTokenBalances(addr)
	out := make(map[string]string, len(balances))
	for sym, bal := range balances {
		out[sym] = bal.String()
	}
	jsonOK(w, map[string]interface{}{"address": addr, "balances": out})
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
		count := 0
		if s.p2p != nil {
			count = s.p2p.PeerCount()
		}
		resp.Result = fmt.Sprintf("0x%x", count)

	case "net_enode":
		if s.p2p == nil || s.p2p.Enode() == "" {
			resp.Error = map[string]interface{}{
				"code":    -32001,
				"message": "P2P advertised host is not configured; set GYDS_P2P_ADVERTISE_HOST",
			}
		} else {
			resp.Result = s.p2p.Enode()
		}

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

// ── Nodes Import ──────────────────────────────────────────────────────────────

type nodesConfig struct {
	Version string      `json:"version"`
	Nodes   []nodeEntry `json:"nodes"`
}

type nodeEntry struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Address string `json:"address"`
	RPC     string `json:"rpc"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

func (e nodeEntry) p2pAddr() string {
	if e.Address != "" {
		return e.Address
	}
	if e.Host != "" && e.Port > 0 {
		return fmt.Sprintf("%s:%d", e.Host, e.Port)
	}
	return ""
}

func (s *Server) handleNodesImport(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var cfg nodesConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(cfg.Nodes) == 0 {
		jsonErr(w, http.StatusBadRequest, "no nodes found in config")
		return
	}

	type result struct {
		Address string `json:"address"`
		Name    string `json:"name"`
		Status  string `json:"status"`
		Error   string `json:"error,omitempty"`
	}

	results := make([]result, 0, len(cfg.Nodes))
	connected := 0

	for _, node := range cfg.Nodes {
		addr := node.p2pAddr()
		name := node.Name
		if name == "" {
			name = node.Type
		}
		if name == "" {
			name = "node"
		}

		res := result{Address: addr, Name: name}

		if addr == "" {
			res.Status = "skipped"
			res.Error = "no address or host/port provided"
			results = append(results, res)
			continue
		}

		if s.p2p != nil {
			if err := s.p2p.ConnectTo(addr); err != nil {
				res.Status = "failed"
				res.Error = err.Error()
			} else {
				res.Status = "connected"
				connected++
			}
		} else {
			res.Status = "queued"
		}
		results = append(results, res)
	}

	jsonOK(w, map[string]interface{}{
		"ok":        true,
		"version":   cfg.Version,
		"total":     len(cfg.Nodes),
		"connected": connected,
		"results":   results,
	})
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
