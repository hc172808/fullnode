package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

const maxRemoteNodeJSONBytes = 4096

type remoteNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type remoteNodeRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func normalizeRemoteNodeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("enter a complete HTTPS dashboard origin")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("remote Admin dashboards must use HTTPS")
	}
	if u.User != nil {
		return "", fmt.Errorf("URLs with embedded usernames or passwords are not allowed")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("enter the dashboard origin only, without a path, query, or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("the HTTPS URL must include a hostname")
	}
	port := u.Port()
	if strings.Contains(u.Host, ":") && port == "" && net.ParseIP(u.Hostname()) == nil {
		return "", fmt.Errorf("the URL contains an invalid port")
	}
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("the URL port must be between 1 and 65535")
		}
	}
	if strings.Contains(host, ":") {
		if port != "" {
			host = net.JoinHostPort(host, port)
		} else {
			host = "[" + host + "]"
		}
	} else if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return "https://" + host, nil
}

func remoteNodeID(origin string) string {
	sum := sha256.Sum256([]byte(origin))
	return hex.EncodeToString(sum[:8])
}

func (s *Server) remoteNodeFile() (string, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return "", fmt.Errorf("node data directory is not configured")
	}
	return filepath.Join(s.dataDir, "admin", "remote-nodes.json"), nil
}

func (s *Server) loadRemoteNodes() ([]remoteNode, error) {
	path, err := s.remoteNodeFile()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []remoteNode{}, nil
	}
	if err != nil {
		return nil, err
	}
	var nodes []remoteNode
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, fmt.Errorf("invalid remote-node registry: %w", err)
	}
	if nodes == nil {
		nodes = []remoteNode{}
	}
	for i := range nodes {
		origin, err := normalizeRemoteNodeURL(nodes[i].URL)
		if err != nil || origin != nodes[i].URL ||
			nodes[i].ID != remoteNodeID(origin) ||
			strings.TrimSpace(nodes[i].Name) == "" || len(nodes[i].Name) > 64 {
			return nil, fmt.Errorf("invalid entry in remote-node registry")
		}
	}
	return nodes, nil
}

func (s *Server) saveRemoteNodes(nodes []remoteNode) error {
	path, err := s.remoteNodeFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}

func (s *Server) handleRemoteNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.remoteNodesMu.Lock()
		nodes, err := s.loadRemoteNodes()
		s.remoteNodesMu.Unlock()
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "could not load remote-node registry")
			return
		}
		jsonOK(w, map[string]interface{}{"nodes": nodes})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxRemoteNodeJSONBytes)
		var input remoteNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid remote-node request")
			return
		}
		name := strings.TrimSpace(input.Name)
		if name == "" || len(name) > 64 {
			jsonErr(w, http.StatusBadRequest, "node name must be between 1 and 64 characters")
			return
		}
		origin, err := normalizeRemoteNodeURL(input.URL)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		node := remoteNode{ID: remoteNodeID(origin), Name: name, URL: origin}

		s.remoteNodesMu.Lock()
		nodes, err := s.loadRemoteNodes()
		if err == nil {
			for _, existing := range nodes {
				if existing.URL == origin {
					err = fmt.Errorf("that HTTPS origin is already registered")
					break
				}
			}
		}
		if err == nil {
			nodes = append(nodes, node)
			err = s.saveRemoteNodes(nodes)
		}
		s.remoteNodesMu.Unlock()
		if err != nil {
			status := http.StatusInternalServerError
			if err.Error() == "that HTTPS origin is already registered" {
				status = http.StatusConflict
			}
			jsonErr(w, status, err.Error())
			return
		}
		jsonOK(w, map[string]interface{}{"node": node})
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRemoteNodeDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(mux.Vars(r)["id"])
	if id == "" {
		jsonErr(w, http.StatusBadRequest, "remote node ID is required")
		return
	}
	s.remoteNodesMu.Lock()
	nodes, err := s.loadRemoteNodes()
	if err == nil {
		filtered := make([]remoteNode, 0, len(nodes))
		for _, node := range nodes {
			if node.ID != id {
				filtered = append(filtered, node)
			}
		}
		if len(filtered) == len(nodes) {
			err = os.ErrNotExist
		} else {
			err = s.saveRemoteNodes(filtered)
		}
	}
	s.remoteNodesMu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		jsonErr(w, http.StatusNotFound, "remote node not found")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not update remote-node registry")
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true})
}
