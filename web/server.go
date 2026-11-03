// Package web is the HTTP layer: it serves the UI and forwards requests to
// the engine. It is stateless; every request carries the full XML.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"

	"github.com/gatogato999/soapviz/config"
	"github.com/gatogato999/soapviz/engine"
	"github.com/gatogato999/soapviz/shared"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	tmpl   *template.Template
	cfg    config.Config
	shared *shared.Store // nil when team samples are disabled
}

func New(cfg config.Config) (*Server, error) {
	t, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{tmpl: t, cfg: cfg}
	if cfg.SharedDir != "" {
		st, err := shared.Open(cfg.SharedDir, cfg.SharedWrite, int64(cfg.MaxBodyMB)<<20)
		if err != nil {
			return nil, err
		}
		s.shared = st
		if _, skipped, err := st.List(); err == nil {
			for _, f := range skipped {
				log.Printf("team samples: ignoring %s", f)
			}
		}
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/config", s.clientConfig)
	mux.HandleFunc("POST /api/process", s.process)
	// Team samples. Writes are PUT and DELETE with JSON bodies, which a web page
	// on another site cannot send without a CORS preflight that this server
	// never grants. That matters because there is no authentication.
	mux.HandleFunc("GET /api/shared", s.sharedList)
	mux.HandleFunc("GET /api/shared/sample", s.sharedGet)
	mux.HandleFunc("PUT /api/shared/sample", s.sharedPut)
	mux.HandleFunc("DELETE /api/shared/sample", s.sharedDelete)
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
	type sharedCfg struct {
		Enabled  bool `json:"enabled"`
		Writable bool `json:"writable"`
	}
	writeJSON(w, http.StatusOK, struct {
		Theme  string            `json:"theme"`
		Gen    config.Generator  `json:"gen"`
		Tags   map[string]string `json:"tags"`
		Shared sharedCfg         `json:"shared"`
	}{s.cfg.Theme, s.cfg.Gen, s.cfg.TagColors, sharedCfg{s.shared != nil, s.shared != nil && s.cfg.SharedWrite}})
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

// --- team samples ---

type apiError struct {
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`      // exists | conflict
	UpdatedAt int64  `json:"updatedAt,omitempty"` // current version, with code "conflict"
}

func (s *Server) sharedEnabled(w http.ResponseWriter) bool {
	if s.shared == nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "team samples are not enabled on this server"})
		return false
	}
	return true
}

func (s *Server) sharedWritable(w http.ResponseWriter) bool {
	if !s.sharedEnabled(w) {
		return false
	}
	if !s.cfg.SharedWrite {
		writeJSON(w, http.StatusForbidden, apiError{Error: "team samples are read-only on this server"})
		return false
	}
	return true
}

// sharedFail maps store errors to HTTP statuses. Unexpected errors are logged
// and reported generically so file paths never reach the browser.
func sharedFail(w http.ResponseWriter, err error) {
	var ce *shared.ConflictError
	switch {
	case errors.Is(err, shared.ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{Error: err.Error()})
	case errors.Is(err, shared.ErrExists):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error(), Code: "exists"})
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error(), Code: "conflict", UpdatedAt: ce.UpdatedAt})
	case errors.Is(err, shared.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{Error: err.Error()})
	case errors.Is(err, shared.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
	default:
		log.Printf("team samples: %v", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "could not access the team sample folder"})
	}
}

func (s *Server) sharedList(w http.ResponseWriter, r *http.Request) {
	if !s.sharedEnabled(w) {
		return
	}
	infos, _, err := s.shared.List()
	if err != nil {
		sharedFail(w, err)
		return
	}
	if infos == nil {
		infos = []shared.Info{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Samples []shared.Info `json:"samples"`
	}{infos})
}

func (s *Server) sharedGet(w http.ResponseWriter, r *http.Request) {
	if !s.sharedEnabled(w) {
		return
	}
	smp, err := s.shared.Get(r.URL.Query().Get("name"))
	if err != nil {
		sharedFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, smp)
}

func (s *Server) sharedPut(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxBodyMB)<<20+4096)
	var req struct {
		Tag         string `json:"tag"`
		XML         string `json:"xml"`
		IfUpdatedAt *int64 `json:"ifUpdatedAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad request: " + err.Error()})
		return
	}
	name := r.URL.Query().Get("name")
	info, err := s.shared.Put(name, req.Tag, req.XML, req.IfUpdatedAt)
	if err != nil {
		sharedFail(w, err)
		return
	}
	// There is no authentication, so leave a trail of who changed what (never the content).
	log.Printf("team samples: wrote %q (%d bytes) from %s", name, info.Bytes, r.RemoteAddr)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) sharedDelete(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	name := r.URL.Query().Get("name")
	if err := s.shared.Delete(name); err != nil {
		sharedFail(w, err)
		return
	}
	log.Printf("team samples: deleted %q from %s", name, r.RemoteAddr)
	w.WriteHeader(http.StatusNoContent)
}
