package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gydschain/fullnode/config"
	"github.com/gydschain/fullnode/p2p"
)

var validNodeModes = map[string]bool{
	"full": true, "lite": true, "rpc": true, "boost": true,
	"genesis": true, "sync": true, "validator": true, "testnode": true,
}

type adminNodeConfig struct {
	ChainID          int64  `json:"chainId"`
	NetworkName      string `json:"networkName"`
	NodeMode         string `json:"nodeMode"`
	BlockTime        int    `json:"blockTime"`
	RPCPort          int    `json:"rpcPort"`
	WSPort           int    `json:"wsPort"`
	P2PPort          int    `json:"p2pPort"`
	P2PAdvertiseHost string `json:"p2pAdvertiseHost"`
	MaxPeers         int    `json:"maxPeers"`
	BootstrapNodes   string `json:"bootstrapNodes"`
	DataDir          string `json:"dataDir"`
	LogLevel         string `json:"logLevel"`
	LogFormat        string `json:"logFormat"`
	PeerAuth         bool   `json:"peerAuth"`
	AllowedNodes     string `json:"allowedNodes"`
	ValidatorKeySet  bool   `json:"validatorKeySet"`
	WalletConfigured bool   `json:"walletConfigured"`
	WalletAddress    string `json:"walletAddress"`
	ValidatorKey     string `json:"validatorKey,omitempty"`
	WalletPrivateKey string `json:"walletPrivateKey,omitempty"`
}

func (s *Server) handleAdminNodePage(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || !s.auth.ValidSession(cookie.Value) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	serveStaticPage(w, "static/admin-node.html")
}

func (s *Server) handleAdminNodeConfig(w http.ResponseWriter, r *http.Request) {
	cfg := config.FromEnv()
	jsonOK(w, adminNodeConfig{
		ChainID: cfg.ChainID, NetworkName: cfg.NetworkName, NodeMode: cfg.NodeMode,
		BlockTime: int(cfg.BlockTime.Seconds()), RPCPort: cfg.RPCPort, WSPort: cfg.WSPort,
		P2PPort: cfg.P2PPort, P2PAdvertiseHost: cfg.P2PAdvertiseHost, MaxPeers: cfg.MaxPeers,
		BootstrapNodes: strings.Join(cfg.P2PBootstrap, ", "), DataDir: cfg.DataDir,
		LogLevel: cfg.LogLevel, LogFormat: cfg.LogFormat, PeerAuth: cfg.PeerAuth,
		AllowedNodes:     strings.Join(cfg.AllowedNodes, ", "),
		ValidatorKeySet:  strings.TrimSpace(os.Getenv("GYDS_VALIDATOR_KEY")) != "",
		WalletConfigured: strings.TrimSpace(os.Getenv("GYDS_WALLET_PRIVATE_KEY")) != "",
		WalletAddress:    strings.TrimSpace(os.Getenv("GYDS_WALLET_ADDRESS")),
	})
}

func validPort(name string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return nil
}

func (c adminNodeConfig) validate() error {
	c.NodeMode = strings.ToLower(strings.TrimSpace(c.NodeMode))
	if !validNodeModes[c.NodeMode] {
		return fmt.Errorf("unsupported node mode %q", c.NodeMode)
	}
	if c.NodeMode == "sync" && strings.TrimSpace(c.BootstrapNodes) == "" {
		return fmt.Errorf("sync mode requires at least one bootstrap node in host:port form")
	}
	if c.ChainID <= 0 {
		return fmt.Errorf("chain ID must be positive")
	}
	if strings.TrimSpace(c.NetworkName) == "" {
		return fmt.Errorf("network name is required")
	}
	if c.BlockTime < 1 {
		return fmt.Errorf("block time must be at least 1 second")
	}
	for _, p := range []struct {
		name string
		port int
	}{{"RPC port", c.RPCPort}, {"WebSocket port", c.WSPort}, {"P2P port", c.P2PPort}} {
		if err := validPort(p.name, p.port); err != nil {
			return err
		}
	}
	if c.MaxPeers < 1 || c.MaxPeers > 10000 {
		return fmt.Errorf("max peers must be between 1 and 10000")
	}
	if strings.ContainsAny(c.DataDir, "\r\n") || strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("data directory must be a non-empty single-line path")
	}
	return nil
}

// updateEnvFile changes only the node settings. Existing secrets and operator
// comments remain intact, and the file stays owner-readable only.
func updateEnvFile(updates map[string]string) error {
	raw, err := os.ReadFile(".env")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	seen := make(map[string]bool, len(updates))
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for key, value := range updates {
			if strings.HasPrefix(trimmed, key+"=") {
				lines[i] = envSetting(key, value)
				seen[key] = true
				break
			}
		}
	}
	for key, value := range updates {
		if !seen[key] {
			lines = append(lines, envSetting(key, value))
		}
	}
	content := strings.Join(lines, "\n") + "\n"
	tmp := ".env.admin.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, ".env"); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(".env", 0600)
}

// persistBootstrapNode keeps an operator-added peer across process restarts.
// The live P2P connection alone is not enough because the process rebuilds its
// peer list from GYDS_BOOTSTRAP_NODES on startup.
func persistBootstrapNode(addr string) error {
	addr = p2p.NormalizeAddr(addr)
	if addr == "" {
		return fmt.Errorf("peer address is required")
	}

	cfg := config.FromEnv()
	for _, existing := range cfg.P2PBootstrap {
		if p2p.NormalizeAddr(existing) == addr {
			return nil
		}
	}

	peers := append(append([]string(nil), cfg.P2PBootstrap...), addr)
	if err := config.SaveBootstrapNodes(cfg.DataDir, peers); err != nil {
		return err
	}
	return updateEnvFile(map[string]string{
		"GYDS_BOOTSTRAP_NODES": strings.Join(peers, ","),
	})
}

func (s *Server) handleAdminNodeConfigApply(w http.ResponseWriter, r *http.Request) {
	var c adminNodeConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	c.NodeMode = strings.ToLower(strings.TrimSpace(c.NodeMode))
	c.NetworkName = strings.TrimSpace(c.NetworkName)
	c.DataDir = strings.TrimSpace(c.DataDir)
	if err := c.validate(); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	updates := map[string]string{
		"GYDS_CHAIN_ID":           strconv.FormatInt(c.ChainID, 10),
		"GYDS_NETWORK_NAME":       c.NetworkName,
		"GYDS_NODE_MODE":          c.NodeMode,
		"GYDS_BLOCK_TIME":         strconv.Itoa(c.BlockTime),
		"GYDS_RPC_PORT":           strconv.Itoa(c.RPCPort),
		"GYDS_WS_PORT":            strconv.Itoa(c.WSPort),
		"GYDS_P2P_PORT":           strconv.Itoa(c.P2PPort),
		"GYDS_P2P_ADVERTISE_HOST": c.P2PAdvertiseHost,
		"GYDS_MAX_PEERS":          strconv.Itoa(c.MaxPeers),
		"GYDS_BOOTSTRAP_NODES":    c.BootstrapNodes,
		"GYDS_DATA_DIR":           c.DataDir,
		"GYDS_LOG_LEVEL":          c.LogLevel,
		"GYDS_LOG_FORMAT":         c.LogFormat,
		"GYDS_PEER_AUTH":          strconv.FormatBool(c.PeerAuth),
		"GYDS_ALLOWED_NODES":      c.AllowedNodes,
	}
	if strings.TrimSpace(c.ValidatorKey) != "" {
		updates["GYDS_VALIDATOR_KEY"] = strings.TrimSpace(c.ValidatorKey)
	}
	if strings.TrimSpace(c.WalletAddress) != "" {
		updates["GYDS_WALLET_ADDRESS"] = strings.TrimSpace(c.WalletAddress)
	}
	if strings.TrimSpace(c.WalletPrivateKey) != "" {
		updates["GYDS_WALLET_PRIVATE_KEY"] = strings.TrimSpace(c.WalletPrivateKey)
	}
	if err := updateEnvFile(updates); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save node configuration: "+err.Error())
		return
	}
	if err := config.SaveBootstrapNodes(c.DataDir, strings.Split(c.BootstrapNodes, ",")); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not persist bootstrap peers: "+err.Error())
		return
	}

	// The process is replaced after the response is flushed. Passing the
	// changed values explicitly is important because the Replit launcher
	// sourced .env before starting the current process.
	jsonOK(w, map[string]interface{}{
		"ok": true, "restarting": true,
		"message": "Configuration saved. The node is restarting with the selected mode.",
	})
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := restartWithUpdates(updates); err != nil {
			// If exec fails, the existing process remains alive and its logs
			// contain a useful diagnostic for the operator.
			fmt.Fprintf(os.Stderr, "node restart failed: %v\n", err)
		}
	}()
}

func restartWithUpdates(updates map[string]string) error {
	env := os.Environ()
	for key, value := range updates {
		prefix := key + "="
		replaced := false
		for i, item := range env {
			if strings.HasPrefix(item, prefix) {
				env[i] = prefix + value
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, prefix+value)
		}
	}
	return syscall.Exec(os.Args[0], os.Args, env)
}

type adminPeerAction struct {
	Address string `json:"address"`
}

func (s *Server) handleAdminNodeConnect(w http.ResponseWriter, r *http.Request) {
	if s.p2p == nil {
		jsonErr(w, http.StatusConflict, "this node mode does not run P2P")
		return
	}
	var action adminPeerAction
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	action.Address = p2p.NormalizeAddr(action.Address)
	if action.Address == "" {
		jsonErr(w, http.StatusBadRequest, "peer address is required (host:port)")
		return
	}
	// Save first. A peer may be restarting or temporarily offline; it must
	// still be retried automatically after this node restarts.
	if err := persistBootstrapNode(action.Address); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not persist peer: "+err.Error())
		return
	}
	if err := s.p2p.ConnectTo(action.Address); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Connection attempt started", "peers": s.p2p.Peers()})
}

func (s *Server) handleAdminNodeSync(w http.ResponseWriter, r *http.Request) {
	if s.p2p == nil {
		jsonErr(w, http.StatusConflict, "this node mode does not run P2P; choose full, lite, sync, boost, genesis, or validator")
		return
	}
	var action adminPeerAction
	_ = json.NewDecoder(r.Body).Decode(&action)
	if strings.TrimSpace(action.Address) != "" {
		action.Address = p2p.NormalizeAddr(action.Address)
		if err := persistBootstrapNode(action.Address); err != nil {
			jsonErr(w, http.StatusInternalServerError, "could not persist peer: "+err.Error())
			return
		}
		if err := s.p2p.ConnectTo(action.Address); err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	jsonOK(w, map[string]interface{}{
		"ok":      true,
		"message": "Sync/connect requested. Sync mode performs full catch-up after restart; connected peers are shown below.",
		"peers":   s.p2p.Peers(),
	})
}
