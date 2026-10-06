package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestNormalizeRemoteNodeURLRequiresHTTPSOrigin(t *testing.T) {
	got, err := normalizeRemoteNodeURL(" HTTPS://Boost.NetlifeGY.com/ ")
	if err != nil {
		t.Fatalf("normalize valid origin: %v", err)
	}
	if got != "https://boost.netlifegy.com" {
		t.Fatalf("normalized origin = %q", got)
	}

	for _, input := range []string{
		"http://boost.netlifegy.com",
		"https://user:pass@boost.netlifegy.com",
		"https://boost.netlifegy.com/admin",
		"https://boost.netlifegy.com/?token=x",
		"https://boost.netlifegy.com/#login",
		"https://boost.netlifegy.com:70000",
	} {
		if _, err := normalizeRemoteNodeURL(input); err == nil {
			t.Errorf("normalizeRemoteNodeURL(%q) unexpectedly succeeded", input)
		}
	}
}

func TestRemoteNodeRegistryRequiresSessionAndPersistsHTTPSLinks(t *testing.T) {
	dataDir := t.TempDir()
	auth := NewAuthStore(dataDir)
	s := &Server{dataDir: dataDir, auth: auth}
	handler := s.requireAdminSession(s.handleRemoteNodes)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/admin/remote-nodes", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized GET status = %d, want 401", unauthorized.Code)
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/remote-nodes", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.NewSession()})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	created := post(`{"name":"Boost Node","url":"https://Boost.NetlifeGY.com/"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Node remoteNode `json:"node"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createdBody.Node.Name != "Boost Node" || createdBody.Node.URL != "https://boost.netlifegy.com" {
		t.Fatalf("created node = %+v", createdBody.Node)
	}
	if got := post(`{"name":"Duplicate","url":"https://boost.netlifegy.com"}`).Code; got != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", got)
	}

	path := filepath.Join(dataDir, "admin", "remote-nodes.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat registry file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("registry mode = %o, want 600", info.Mode().Perm())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/remote-nodes", nil)
	getReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.NewSession()})
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getReq)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), "boost.netlifegy.com") {
		t.Fatalf("GET status/body = %d/%s", getResponse.Code, getResponse.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/admin/remote-nodes/"+createdBody.Node.ID, nil)
	deleteReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.NewSession()})
	deleteReq = mux.SetURLVars(deleteReq, map[string]string{"id": createdBody.Node.ID})
	deleteResponse := httptest.NewRecorder()
	s.requireAdminSession(s.handleRemoteNodeDelete).ServeHTTP(deleteResponse, deleteReq)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	nodes, err := s.loadRemoteNodes()
	if err != nil {
		t.Fatalf("load registry after delete: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("remote registry has %d nodes after delete, want 0", len(nodes))
	}
}
