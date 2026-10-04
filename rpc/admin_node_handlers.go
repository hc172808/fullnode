package rpc

import (
	"encoding/json"
	"fmt"
	"net"
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
	Network          string `json:"network"`
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
	bootstrapPeers := applyStagedBootstrapChanges(cfg.P2PBootstrap, s.pendingBootstrapChanges(cfg.Network))
	jsonOK(w, adminNodeConfig{
		Network: cfg.Network, ChainID: cfg.ChainID, NetworkName: cfg.NetworkName, NodeMode: cfg.NodeMode,
		BlockTime: int(cfg.BlockTime.Seconds()), RPCPort: cfg.RPCPort, WSPort: cfg.WSPort,
		P2PPort: cfg.P2PPort, P2PAdvertiseHost: cfg.P2PAdvertiseHost, MaxPeers: cfg.MaxPeers,
		BootstrapNodes: strings.Join(bootstrapPeers, ", "), DataDir: cfg.DataDir,
		LogLevel: cfg.LogLevel, LogFormat: cfg.LogFormat, PeerAuth: cfg.PeerAuth,
		AllowedNodes:     strings.Join(cfg.AllowedNodes, ", "),
		ValidatorKeySet:  strings.TrimSpace(os.Getenv("GYDS_VALIDATOR_KEY")) != "",
		WalletConfigured: strings.TrimSpace(os.Getenv("GYDS_WALLET_PRIVATE_KEY")) != "",
		WalletAddress:    strings.TrimSpace(os.Getenv("GYDS_WALLET_ADDRESS")),
	})
}

// handleAdminNodeStatus returns the live presence and synchronization view used
// by the operator panel. Peer heights are exchanged during the P2P handshake,
// so this deliberately reports connected peers rather than stale configured
// bootstrap addresses.
func (s *Server) handleAdminNodeStatus(w http.ResponseWriter, r *http.Request) {
	localHeight := s.chain.Height()
	peers := []p2p.PeerStatus{}
	if s.p2p != nil {
		peers = s.p2p.Peers()
	}

	byMode := make(map[string]int)
	highestPeerHeight := localHeight
	for _, peer := range peers {
		mode := strings.ToLower(strings.TrimSpace(peer.NodeMode))
		if mode == "" {
			mode = "unknown"
		}
		byMode[mode]++
		if peer.Height > highestPeerHeight {
			highestPeerHeight = peer.Height
		}
	}

	syncStatus := "no-peers"
	if len(peers) > 0 {
		syncStatus = "synced"
		if highestPeerHeight > localHeight {
			syncStatus = "syncing"
		}
	}
	jsonOK(w, map[string]interface{}{
		"nodeMode":          s.nodeMode,
		"localHeight":       localHeight,
		"highestPeerHeight": highestPeerHeight,
		"blocksBehind":      highestPeerHeight - localHeight,
		"syncStatus":        syncStatus,
		"connected":         len(peers),
		"nodeTypes":         byMode,
		"checkedAt":         time.Now().UTC().Format(time.RFC3339),
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
	bootstrapPeers, err := parseBootstrapPeers(c.BootstrapNodes)
	if err != nil {
		return err
	}
	if c.NodeMode == "sync" && len(bootstrapPeers) == 0 {
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

type stagedBootstrapChanges struct {
	Added   []string
	Removed []string
}

func bootstrapProfile(network string) string {
	if strings.EqualFold(strings.TrimSpace(network), "testnet") {
		return "testnet"
	}
	return "mainnet"
}

func normalizeBootstrapPeer(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	addr = p2p.NormalizeAddr(addr)
	host, rawPort, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("peer address %q must be host:port", addr)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("peer address %q must use a port from 1 to 65535", addr)
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)), nil
}

func parseBootstrapPeers(raw string) ([]string, error) {
	peers := make([]string, 0)
	seen := make(map[string]bool)
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		peer, err := normalizeBootstrapPeer(value)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(peer)
		if !seen[key] {
			seen[key] = true
			peers = append(peers, peer)
		}
	}
	return peers, nil
}

func mergeBootstrapPeers(base, additions []string) []string {
	out := make([]string, 0, len(base)+len(additions))
	seen := make(map[string]bool)
	for _, peer := range append(append([]string(nil), base...), additions...) {
		peer = p2p.NormalizeAddr(peer)
		if peer == "" {
			continue
		}
		key := strings.ToLower(peer)
		if !seen[key] {
			seen[key] = true
			out = append(out, peer)
		}
	}
	return out
}

func applyStagedBootstrapChanges(base []string, changes stagedBootstrapChanges) []string {
	removed := make(map[string]bool, len(changes.Removed))
	for _, peer := range changes.Removed {
		removed[strings.ToLower(p2p.NormalizeAddr(peer))] = true
	}
	kept := make([]string, 0, len(base))
	for _, peer := range base {
		if !removed[strings.ToLower(p2p.NormalizeAddr(peer))] {
			kept = append(kept, peer)
		}
	}
	return mergeBootstrapPeers(kept, changes.Added)
}

func (s *Server) pendingBootstrapChanges(network string) stagedBootstrapChanges {
	profile := bootstrapProfile(network)
	s.pendingBootstrapMu.Lock()
	defer s.pendingBootstrapMu.Unlock()
	change := s.pendingBootstrap[profile]
	return stagedBootstrapChanges{
		Added:   append([]string(nil), change.Added...),
		Removed: append([]string(nil), change.Removed...),
	}
}

func (s *Server) stageBootstrapPeer(network, rawAddress string) error {
	address, err := normalizeBootstrapPeer(rawAddress)
	if err != nil {
		return err
	}
	profile := bootstrapProfile(network)
	s.pendingBootstrapMu.Lock()
	defer s.pendingBootstrapMu.Unlock()
	if s.pendingBootstrap == nil {
		s.pendingBootstrap = make(map[string]stagedBootstrapChanges)
	}
	change := s.pendingBootstrap[profile]
	addressKey := strings.ToLower(address)
	for i, peer := range change.Removed {
		if strings.ToLower(p2p.NormalizeAddr(peer)) == addressKey {
			change.Removed = append(change.Removed[:i], change.Removed[i+1:]...)
			break
		}
	}
	for _, peer := range change.Added {
		if strings.ToLower(p2p.NormalizeAddr(peer)) == addressKey {
			s.pendingBootstrap[profile] = change
			return nil
		}
	}
	change.Added = append(change.Added, address)
	s.pendingBootstrap[profile] = change
	return nil
}

func (s *Server) stageBootstrapRemoval(network, rawAddress string) error {
	address, err := normalizeBootstrapPeer(rawAddress)
	if err != nil {
		return err
	}
	profile := bootstrapProfile(network)
	s.pendingBootstrapMu.Lock()
	defer s.pendingBootstrapMu.Unlock()
	if s.pendingBootstrap == nil {
		s.pendingBootstrap = make(map[string]stagedBootstrapChanges)
	}
	change := s.pendingBootstrap[profile]
	addressKey := strings.ToLower(address)
	for i, peer := range change.Added {
		if strings.ToLower(p2p.NormalizeAddr(peer)) == addressKey {
			change.Added = append(change.Added[:i], change.Added[i+1:]...)
			break
		}
	}
	for _, peer := range change.Removed {
		if strings.ToLower(p2p.NormalizeAddr(peer)) == addressKey {
			return nil
		}
	}
	change.Removed = append(change.Removed, address)
	s.pendingBootstrap[profile] = change
	return nil
}

func (s *Server) clearPendingBootstrapChanges(network string) {
	s.pendingBootstrapMu.Lock()
	defer s.pendingBootstrapMu.Unlock()
	delete(s.pendingBootstrap, bootstrapProfile(network))
}

// updateEnvFile changes only the node settings. Existing secrets and operator
// comments remain intact, and the file stays owner-readable only.
func updateEnvFile(updates map[string]string) error {
	envPath := envFilePath()
	raw, err := os.ReadFile(envPath)
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
	tmp := envPath + ".admin.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, envPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(envPath, 0600)
}

func (s *Server) handleAdminNodeConfigApply(w http.ResponseWriter, r *http.Request) {
	var c adminNodeConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	c.NodeMode = strings.ToLower(strings.TrimSpace(c.NodeMode))
	c.Network = strings.ToLower(strings.TrimSpace(c.Network))
	c.DataDir = strings.TrimSpace(c.DataDir)
	switch c.Network {
	case "mainnet":
		c.ChainID = 198282
		c.NetworkName = "GYDS Chain"
	case "testnet":
		if c.NodeMode == "testnode" {
			jsonErr(w, http.StatusBadRequest, "the disposable Test Node always uses chain ID 31337; select another node mode for the persistent testnet")
			return
		}
		c.ChainID = 198281
		c.NetworkName = "GYDS Testnet"
	default:
		jsonErr(w, http.StatusBadRequest, "network must be mainnet or testnet")
		return
	}
	c.DataDir = config.ProfileDataDir(c.Network, c.DataDir)
	if err := c.validate(); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	bootstrapPeers, err := parseBootstrapPeers(c.BootstrapNodes)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	updates := map[string]string{
		"GYDS_NETWORK":            c.Network,
		"GYDS_CHAIN_ID":           strconv.FormatInt(c.ChainID, 10),
		"GYDS_NETWORK_NAME":       c.NetworkName,
		"GYDS_NODE_MODE":          c.NodeMode,
		"GYDS_BLOCK_TIME":         strconv.Itoa(c.BlockTime),
		"GYDS_RPC_PORT":           strconv.Itoa(c.RPCPort),
		"GYDS_WS_PORT":            strconv.Itoa(c.WSPort),
		"GYDS_P2P_PORT":           strconv.Itoa(c.P2PPort),
		"GYDS_P2P_ADVERTISE_HOST": c.P2PAdvertiseHost,
		"GYDS_MAX_PEERS":          strconv.Itoa(c.MaxPeers),
		"GYDS_BOOTSTRAP_NODES":    strings.Join(bootstrapPeers, ","),
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
	if err := config.SaveBootstrapNodes(c.DataDir, bootstrapPeers); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not persist bootstrap peers: "+err.Error())
		return
	}
	s.clearPendingBootstrapChanges(c.Network)

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
	var action adminPeerAction
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	action.Address = p2p.NormalizeAddr(action.Address)
	if err := s.stageBootstrapPeer(config.FromEnv().Network, action.Address); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Peer staged. Click Apply & Restart Node to save and connect it."})
}

func (s *Server) handleAdminNodeSync(w http.ResponseWriter, r *http.Request) {
	var action adminPeerAction
	_ = json.NewDecoder(r.Body).Decode(&action)
	if strings.TrimSpace(action.Address) != "" {
		if err := s.stageBootstrapPeer(config.FromEnv().Network, action.Address); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	jsonOK(w, map[string]interface{}{
		"ok":      true,
		"message": "Peer changes are staged. Click Apply & Restart Node to save them and reconnect.",
	})
}

func (s *Server) handleAdminNodeRemove(w http.ResponseWriter, r *http.Request) {
	var action adminPeerAction
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.stageBootstrapRemoval(config.FromEnv().Network, action.Address); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{
		"ok": true, "message": "Peer removal staged. Click Apply & Restart Node to save the change.",
	})
}
