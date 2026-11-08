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
	mux.HandleFunc("PATCH /api/shared/sample", s.sharedMove)
	mux.HandleFunc("DELETE /api/shared/sample", s.sharedDelete)
	mux.HandleFunc("PUT /api/shared/folder", s.folderCreate)
	mux.HandleFunc("PATCH /api/shared/folder", s.folderRename)
	mux.HandleFunc("DELETE /api/shared/folder", s.folderDelete)
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
	Code      string `json:"code,omitempty"`      // exists | conflict | notempty
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
	case errors.Is(err, shared.ErrNotEmpty):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error(), Code: "notempty"})
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
	l, _, err := s.shared.List()
	if err != nil {
		sharedFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) sharedGet(w http.ResponseWriter, r *http.Request) {
	if !s.sharedEnabled(w) {
		return
	}
	q := r.URL.Query()
	smp, err := s.shared.Get(q.Get("folder"), q.Get("name"))
	if err != nil {
		sharedFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, smp)
}

func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad request: " + err.Error()})
		return false
	}
	return true
}

func (s *Server) sharedPut(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	var req struct {
		Tag         string `json:"tag"`
		XML         string `json:"xml"`
		IfUpdatedAt *int64 `json:"ifUpdatedAt"`
	}
	if !decodeBody(w, r, int64(s.cfg.MaxBodyMB)<<20+4096, &req) {
		return
	}
	q := r.URL.Query()
	info, err := s.shared.Put(q.Get("folder"), q.Get("name"), req.Tag, req.XML, req.IfUpdatedAt)
	if err != nil {
		sharedFail(w, err)
		return
	}
	// There is no authentication, so leave a trail of who changed what (never the content).
	log.Printf("team samples: wrote %q in %q (%d bytes) from %s", info.Name, info.Folder, info.Bytes, r.RemoteAddr)
	writeJSON(w, http.StatusOK, info)
}

// sharedMove renames, retags and/or moves a sample to another folder.
func (s *Server) sharedMove(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	var req struct {
		Folder      string `json:"folder"`
		Name        string `json:"name"`
		Tag         string `json:"tag"`
		IfUpdatedAt *int64 `json:"ifUpdatedAt"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	if req.IfUpdatedAt == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "ifUpdatedAt is required"})
		return
	}
	q := r.URL.Query()
	info, err := s.shared.Move(q.Get("folder"), q.Get("name"), shared.Target{Folder: req.Folder, Name: req.Name, Tag: req.Tag}, *req.IfUpdatedAt)
	if err != nil {
		sharedFail(w, err)
		return
	}
	log.Printf("team samples: moved %q/%q to %q/%q from %s", q.Get("folder"), q.Get("name"), info.Folder, info.Name, r.RemoteAddr)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) sharedDelete(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	q := r.URL.Query()
	if err := s.shared.Delete(q.Get("folder"), q.Get("name")); err != nil {
		sharedFail(w, err)
		return
	}
	log.Printf("team samples: deleted %q in %q from %s", q.Get("name"), q.Get("folder"), r.RemoteAddr)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) folderCreate(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	name := r.URL.Query().Get("name")
	if err := s.shared.CreateFolder(name); err != nil {
		sharedFail(w, err)
		return
	}
	log.Printf("team samples: created folder %q from %s", name, r.RemoteAddr)
	writeJSON(w, http.StatusOK, struct {
		Name string `json:"name"`
	}{name})
}

func (s *Server) folderRename(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	old := r.URL.Query().Get("name")
	if err := s.shared.RenameFolder(old, req.Name); err != nil {
		sharedFail(w, err)
		return
	}
	log.Printf("team samples: renamed folder %q to %q from %s", old, req.Name, r.RemoteAddr)
	writeJSON(w, http.StatusOK, struct {
		Name string `json:"name"`
	}{req.Name})
}

func (s *Server) folderDelete(w http.ResponseWriter, r *http.Request) {
	if !s.sharedWritable(w) {
		return
	}
	name := r.URL.Query().Get("name")
	if err := s.shared.DeleteFolder(name); err != nil {
		sharedFail(w, err)
		return
	}
	log.Printf("team samples: deleted folder %q from %s", name, r.RemoteAddr)
	w.WriteHeader(http.StatusNoContent)
}
