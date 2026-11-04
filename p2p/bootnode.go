package p2p

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// BootnodeConfig describes a P2P-only discovery service. Bootnodes keep no
// chain database and do not provide RPC, block production, or validator duties.
type BootnodeConfig struct {
	DataDir      string
	NodeKeyPath  string
	Port         int
	MaxPeers     int
	ChainID      int64
	GenesisHash  string
	PublicAddr   string
	Bootstrap    []string
	PeerAuth     bool
	AllowedNodes []string
}

// Bootnode wraps the shared peer server with a discovery-only runtime.
type Bootnode struct {
	server    *Server
	bootstrap []string
	quit      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func NewBootnode(cfg BootnodeConfig) (*Bootnode, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, fmt.Errorf("bootnode data directory is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("bootnode P2P port must be between 1 and 65535")
	}
	if cfg.MaxPeers < 1 || cfg.MaxPeers > 10000 {
		return nil, fmt.Errorf("bootnode max peers must be between 1 and 10000")
	}
	if cfg.ChainID < 1 {
		return nil, fmt.Errorf("bootnode chain ID must be positive")
	}
	if strings.TrimSpace(cfg.GenesisHash) == "" {
		return nil, fmt.Errorf("bootnode genesis hash is required to verify peer network identity")
	}

	keyPath := cfg.NodeKeyPath
	if strings.TrimSpace(keyPath) == "" {
		keyPath = filepath.Join(cfg.DataDir, "node.key")
	}
	key, err := LoadOrCreateNodeKeyAt(keyPath)
	if err != nil {
		return nil, fmt.Errorf("load bootnode identity: %w", err)
	}

	host, publicPort, err := parseBootnodePublicAddr(cfg.PublicAddr)
	if err != nil {
		return nil, err
	}

	server := NewServer(cfg.Port, cfg.ChainID, func() uint64 { return 0 })
	server.SetNodeMode("bootnode")
	server.SetGenesisHash(cfg.GenesisHash)
	server.SetMaxPeers(cfg.MaxPeers)
	server.SetAuth(key, cfg.PeerAuth, cfg.AllowedNodes)
	if host != "" {
		server.SetAdvertiseEndpoint(host, publicPort)
	}

	bootstrap := make([]string, 0, len(cfg.Bootstrap))
	for _, addr := range cfg.Bootstrap {
		addr = NormalizeAddr(addr)
		if !validP2PEndpoint(addr) {
			return nil, fmt.Errorf("invalid bootstrap peer address %q (expected host:port)", addr)
		}
		bootstrap = append(bootstrap, addr)
	}

	return &Bootnode{
		server:    server,
		bootstrap: bootstrap,
		quit:      make(chan struct{}),
	}, nil
}

func parseBootnodePublicAddr(value string) (string, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", 0, nil
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		if !validAdvertiseHost(value) {
			return "", 0, fmt.Errorf("public address must not be a loopback or unspecified address")
		}
		return strings.Trim(value, "[]"), 0, nil
	}
	if host, portText, err := net.SplitHostPort(value); err == nil {
		if !validAdvertiseHost(host) {
			return "", 0, fmt.Errorf("public address host must not be localhost or unspecified")
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("public address port must be between 1 and 65535")
		}
		return host, port, nil
	}
	if strings.Contains(value, ":") || !validAdvertiseHost(value) {
		return "", 0, fmt.Errorf("invalid public address %q (expected a public host or host:port)", value)
	}
	return value, 0, nil
}

// Start opens only the P2P listener and retries configured bootstrap peers.
func (b *Bootnode) Start() error {
	if err := b.server.Start(); err != nil {
		return err
	}
	for _, addr := range b.bootstrap {
		b.wg.Add(1)
		go b.retryBootstrap(addr)
	}
	return nil
}

func (b *Bootnode) retryBootstrap(addr string) {
	defer b.wg.Done()
	delay := time.Second
	for {
		if !b.server.IsConnectedTo(addr) {
			if err := b.server.ConnectTo(addr); err != nil {
				log.Warn().Err(err).Str("addr", addr).Dur("retryIn", delay).Msg("bootnode bootstrap peer unavailable")
			}
		} else {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-b.quit:
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func (b *Bootnode) Close() {
	b.closeOnce.Do(func() {
		close(b.quit)
		b.server.Close()
		b.wg.Wait()
	})
}

func (b *Bootnode) NodeID() string { return b.server.NodeID() }

func (b *Bootnode) P2PPort() int { return b.server.P2PPort() }
