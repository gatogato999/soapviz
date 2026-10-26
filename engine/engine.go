// Package engine is the whole request/response logic of soapviz, with no
// HTTP and no templates, so the same code serves the Go web server and the
// WebAssembly build that runs on GitHub Pages.
package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gatogato999/soapviz/soap"
)

type Request struct {
	XML  string   `json:"xml"`
	Edit *EditReq `json:"edit,omitempty"`
	Gen  GenReq   `json:"gen"`
}

type EditReq struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Index int    `json:"index"`
	Value string `json:"value"`
}

type GenReq struct {
	Envelope   bool   `json:"envelope"` // generate from the whole Envelope instead of the Body payload
	Namespaces bool   `json:"namespaces"`
	JSON       bool   `json:"json"`
	JSONCase   string `json:"jsonCase"` // none | snake | camel
	OmitEmpty  bool   `json:"omitEmpty"`
	Root       string `json:"root"`
	Package    string `json:"pkg"`
}

type Response struct {
	Empty     bool      `json:"empty,omitempty"`
	Error     string    `json:"error,omitempty"`     // parse or edit failure; other fields are unset
	XML       string    `json:"xml,omitempty"`       // normalized XML after any edit
	Tree      *NodeView `json:"tree,omitempty"`      // element tree for the UI
	Code      string    `json:"code,omitempty"`      // generated Go, plain text (for copying)
	CodeHTML  string    `json:"codeHtml,omitempty"`  // same, HTML-escaped with highlight spans
	CodeError string    `json:"codeError,omitempty"` // generator failure
}

// NodeView is one element of the tree the UI renders.
type NodeView struct {
	Path     string     `json:"path"` // child indexes joined by "."; "" is the root
	Prefix   string     `json:"prefix,omitempty"`
	Local    string     `json:"local"`
	Root     bool       `json:"root,omitempty"`
	Leaf     bool       `json:"leaf,omitempty"`
	NS       []NSView   `json:"ns,omitempty"`
	Attrs    []AttrView `json:"attrs,omitempty"`
	Text     string     `json:"text,omitempty"`
	Children []NodeView `json:"children,omitempty"`
}

type NSView struct {
	Decl string `json:"decl"`
	URI  string `json:"uri"`
}

type AttrView struct {
	Index int    `json:"index"` // position in the element's attribute list, for edits
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Process parses the XML, applies at most one edit, and returns the
// normalized XML, the tree and the generated Go. It never panics.
func Process(req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = Response{Error: fmt.Sprintf("internal error: %v", r)}
		}
	}()

	if strings.TrimSpace(req.XML) == "" {
		return Response{Empty: true}
	}
	root, err := soap.Parse(strings.NewReader(req.XML))
	if err != nil {
		return Response{Error: err.Error()}
	}
	if e := req.Edit; e != nil {
		path, err := soap.ParsePath(e.Path)
		if err == nil {
			err = root.Apply(soap.Edit{Op: e.Op, Path: path, Index: e.Index, Value: e.Value})
		}
		if err != nil {
			return Response{Error: err.Error()}
		}
	}

	tree := buildView(root, nil)
	resp = Response{XML: soap.Render(root), Tree: &tree}

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
		resp.CodeHTML = highlightGo(code)
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

func buildView(n *soap.Node, path []int) NodeView {
	v := NodeView{
		Path:   soap.FormatPath(path),
		Prefix: n.Prefix,
		Local:  n.Name,
		Root:   len(path) == 0,
		Leaf:   len(n.Children) == 0,
		Text:   n.Text,
	}
	for i, a := range n.Attrs {
		if a.IsNS() {
			v.NS = append(v.NS, NSView{Decl: a.QName(), URI: a.Value})
			continue
		}
		v.Attrs = append(v.Attrs, AttrView{Index: i, Name: a.QName(), Value: a.Value})
	}
	for i, c := range n.Children {
		v.Children = append(v.Children, buildView(c, append(slices.Clone(path), i)))
	}
	return v
}
