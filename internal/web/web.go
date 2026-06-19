// Package web expõe uma interface HTTP para visualizar, filtrar e consultar
// os logs persistidos pelo slog. A página usa Handlebars (embutido) no cliente
// e faz polling nos endpoints JSON deste servidor.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/IsaacDSC/slog/internal/store"
)

//go:embed assets/*
var assets embed.FS

// Server serve a interface web sobre um store.
type Server struct {
	store *store.Store
	mux   *http.ServeMux
}

// New cria o servidor e registra as rotas.
func New(s *store.Store) *Server {
	srv := &Server{store: s, mux: http.NewServeMux()}

	sub, _ := fs.Sub(assets, "assets")
	srv.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))

	srv.mux.HandleFunc("GET /", srv.handleIndex)
	srv.mux.HandleFunc("GET /api/logs", srv.handleLogs)
	srv.mux.HandleFunc("GET /api/levels", srv.handleLevels)
	srv.mux.HandleFunc("POST /api/query", srv.handleQuery)
	return srv
}

// Handler devolve o http.Handler pronto para uso (útil em testes/montagem).
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.LogFilter{
		Text:  q.Get("q"),
		Level: q.Get("level"),
	}
	if v, err := strconv.ParseInt(q.Get("since_id"), 10, 64); err == nil {
		f.SinceID = v
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = v
	}

	logs, err := s.store.FilterLogs(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var maxID int64
	for _, l := range logs {
		if l.ID > maxID {
			maxID = l.ID
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":  logs,
		"maxId": maxID,
	})
}

func (s *Server) handleLevels(w http.ResponseWriter, r *http.Request) {
	levels, err := s.store.Levels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"levels": levels})
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var body struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido: " + err.Error()})
		return
	}

	res, err := s.store.RunQuery(body.SQL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
	}
}
