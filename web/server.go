// Package web is the HTTP layer: it serves the UI and forwards requests to
// the engine. It is stateless; every request carries the full XML.
package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"

	"soapviz/config"
	"soapviz/engine"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	tmpl *template.Template
	cfg  config.Config
}

func New(cfg config.Config) (*Server, error) {
	t, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{tmpl: t, cfg: cfg}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/config", s.clientConfig)
	mux.HandleFunc("POST /api/process", s.process)
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct{ Theme string }{s.cfg.Theme}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// clientConfig sends the defaults a browser uses until its user saves their
// own. The server keeps no per-user state; saved choices live in the browser.
func (s *Server) clientConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Theme string           `json:"theme"`
		Gen   config.Generator `json:"gen"`
	}{s.cfg.Theme, s.cfg.Gen})
}

func (s *Server) process(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxBodyMB)<<20)
	var req engine.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, engine.Response{Error: "bad request: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, engine.Process(req))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
