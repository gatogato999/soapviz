// Package web is the thin HTTP layer over the soap package. It is stateless:
// every request carries the full XML, so the browser textarea is the only
// source of truth.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"slices"
	"strings"

	"soapviz/soap"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	tmpl *template.Template
}

func New() (*Server, error) {
	t, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{tmpl: t}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /api/process", s.process)
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type processReq struct {
	XML  string   `json:"xml"`
	Edit *editReq `json:"edit,omitempty"`
	Gen  genReq   `json:"gen"`
}

type editReq struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Index int    `json:"index"`
	Value string `json:"value"`
}

type genReq struct {
	Envelope   bool   `json:"envelope"` // generate from the whole Envelope instead of the Body payload
	Namespaces bool   `json:"namespaces"`
	JSON       bool   `json:"json"`
	JSONCase   string `json:"jsonCase"` // none | snake | camel
	OmitEmpty  bool   `json:"omitEmpty"`
	Root       string `json:"root"`
	Package    string `json:"pkg"`
}

type processResp struct {
	Empty     bool   `json:"empty,omitempty"`
	Error     string `json:"error,omitempty"`     // parse or edit failure; other fields are unset
	XML       string `json:"xml,omitempty"`       // normalized XML after any edit
	Tree      string `json:"tree,omitempty"`      // rendered tree HTML
	Code      string `json:"code,omitempty"`      // generated Go
	CodeError string `json:"codeError,omitempty"` // generator failure
}

func (s *Server) process(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var req processReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, processResp{Error: "bad request: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.handle(req))
}

func (s *Server) handle(req processReq) processResp {
	if strings.TrimSpace(req.XML) == "" {
		return processResp{Empty: true}
	}
	root, err := soap.Parse(strings.NewReader(req.XML))
	if err != nil {
		return processResp{Error: err.Error()}
	}
	if e := req.Edit; e != nil {
		path, err := soap.ParsePath(e.Path)
		if err == nil {
			err = root.Apply(soap.Edit{Op: e.Op, Path: path, Index: e.Index, Value: e.Value})
		}
		if err != nil {
			return processResp{Error: err.Error()}
		}
	}

	resp := processResp{XML: soap.Render(root)}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "node", buildView(root, nil)); err != nil {
		return processResp{Error: err.Error()}
	}
	resp.Tree = buf.String()

	target := root
	if !req.Gen.Envelope {
		target = soap.Payload(root)
	}
	code, err := soap.Generate(target, soap.Options{
		Package:    strings.TrimSpace(req.Gen.Package),
		Root:       strings.TrimSpace(req.Gen.Root),
		Namespaces: req.Gen.Namespaces,
		JSON:       req.Gen.JSON,
		JSONCase:   jsonCase(req.Gen.JSONCase),
		OmitEmpty:  req.Gen.OmitEmpty,
	})
	if err != nil {
		resp.CodeError = err.Error()
	} else {
		resp.Code = code
	}
	return resp
}

func jsonCase(s string) soap.Case {
	switch s {
	case "snake":
		return soap.CaseSnake
	case "camel":
		return soap.CaseCamel
	}
	return soap.CaseNone
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- tree view model ---

type nodeView struct {
	Path      string
	Label     string
	NameSize  int
	Root      bool
	Leaf      bool
	NS        []nsView
	Attrs     []attrView
	Text      string
	Multiline bool
	Rows      int
	Children  []nodeView
}

type nsView struct{ Decl, URI string }

type attrView struct {
	Index       int
	Name, Value string
	Size        int
}

func buildView(n *soap.Node, path []int) nodeView {
	label := n.QName()
	v := nodeView{
		Path:     soap.FormatPath(path),
		Label:    label,
		NameSize: clamp(len(label)+1, 3, 60),
		Root:     len(path) == 0,
		Leaf:     len(n.Children) == 0,
		Text:     n.Text,
	}
	if lines := strings.Count(n.Text, "\n") + 1; lines > 1 {
		v.Multiline, v.Rows = true, min(lines, 8)
	}
	for i, a := range n.Attrs {
		if a.IsNS() {
			v.NS = append(v.NS, nsView{Decl: a.QName(), URI: a.Value})
			continue
		}
		v.Attrs = append(v.Attrs, attrView{Index: i, Name: a.QName(), Value: a.Value, Size: clamp(len(a.Value)+1, 4, 40)})
	}
	for i, c := range n.Children {
		v.Children = append(v.Children, buildView(c, append(slices.Clone(path), i)))
	}
	return v
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
