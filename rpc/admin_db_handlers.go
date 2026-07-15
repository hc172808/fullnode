package rpc

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// requireAdminSession is a middleware that rejects requests without a valid session.
func (s *Server) requireAdminSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !s.auth.ValidSession(cookie.Value) {
			jsonErr(w, http.StatusUnauthorized, "admin session required")
			return
		}
		next(w, r)
	}
}

// ── GET /admin/db ─────────────────────────────────────────────────────────────

func (s *Server) handleAdminDBPage(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || !s.auth.ValidSession(cookie.Value) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	serveStaticPage(w, "static/admin-db.html")
}

// ── GET /admin/db/tables ──────────────────────────────────────────────────────

func (s *Server) handleDBTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.adminDB.Tables()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type tableInfo struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	infos := make([]tableInfo, len(tables))
	for i, t := range tables {
		infos[i] = tableInfo{Name: t, Count: s.adminDB.TableCount(t)}
	}
	jsonOK(w, map[string]interface{}{"tables": infos})
}

// ── POST /admin/db/tables ─────────────────────────────────────────────────────

func (s *Server) handleDBCreateTable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		jsonErr(w, http.StatusBadRequest, "field 'name' is required")
		return
	}
	if err := s.adminDB.CreateTable(body.Name); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok", "table": body.Name})
}

// ── DELETE /admin/db/tables/{table} ──────────────────────────────────────────

func (s *Server) handleDBDropTable(w http.ResponseWriter, r *http.Request) {
	table := mux.Vars(r)["table"]
	if err := s.adminDB.DropTable(table); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ── GET /admin/db/tables/{table}/records ─────────────────────────────────────

func (s *Server) handleDBRecords(w http.ResponseWriter, r *http.Request) {
	table := mux.Vars(r)["table"]
	recs, err := s.adminDB.Records(table)
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	if recs == nil {
		recs = []DBRecord{}
	}
	jsonOK(w, map[string]interface{}{"table": table, "records": recs, "count": len(recs)})
}

// ── POST /admin/db/tables/{table}/records ─────────────────────────────────────

func (s *Server) handleDBCreateRecord(w http.ResponseWriter, r *http.Request) {
	table := mux.Vars(r)["table"]
	var body struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Value) == 0 {
		body.Value = json.RawMessage(`{}`)
	}
	key, err := s.adminDB.PutRecord(table, body.Key, body.Value)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok", "key": key})
}

// ── PUT /admin/db/tables/{table}/records/{key} ────────────────────────────────

func (s *Server) handleDBUpdateRecord(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	table, key := vars["table"], vars["key"]
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Value) == 0 {
		jsonErr(w, http.StatusBadRequest, "field 'value' is required")
		return
	}
	if _, err := s.adminDB.PutRecord(table, key, body.Value); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok", "key": key})
}

// ── DELETE /admin/db/tables/{table}/records/{key} ─────────────────────────────

func (s *Server) handleDBDeleteRecord(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	table, key := vars["table"], vars["key"]
	if err := s.adminDB.DeleteRecord(table, key); err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}
