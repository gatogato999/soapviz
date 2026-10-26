package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

const doc = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" xmlns:p="urn:p">
  <s:Body><p:R status="ok"><p:A>1</p:A><p:I n="1"><p:V>x</p:V></p:I><p:I n="2"/></p:R></s:Body>
</s:Envelope>`

func TestProcess(t *testing.T) {
	r := Process(Request{XML: doc, Gen: GenReq{JSON: true, JSONCase: "snake"}})
	if r.Error != "" || r.Tree == nil {
		t.Fatalf("unexpected: %+v", r)
	}
	// Envelope > Body(0) > R(0) > I(1)
	i := r.Tree.Children[0].Children[0].Children[1]
	if i.Path != "0.0.1" || i.Prefix != "p" || i.Local != "I" || len(i.Attrs) != 1 || i.Attrs[0].Name != "n" {
		t.Errorf("bad tree node: %+v", i)
	}
	if len(r.Tree.NS) != 2 || !r.Tree.Root || r.Tree.Children[0].Root {
		t.Errorf("bad root/ns: %+v", r.Tree)
	}
	if !strings.Contains(r.Code, "type R struct") || !strings.Contains(r.Code, `json:"a"`) || !strings.Contains(r.CodeHTML, `<span class="kw">`) {
		t.Errorf("bad code:\n%s", r.Code)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
}

func TestProcessEnvelopeOption(t *testing.T) {
	r := Process(Request{XML: doc, Gen: GenReq{Envelope: true}})
	if !strings.Contains(r.Code, "type Envelope struct") {
		t.Errorf("expected Envelope type:\n%s", r.Code)
	}
}

func TestProcessEditAndErrors(t *testing.T) {
	r := Process(Request{XML: doc, Edit: &EditReq{Op: "text", Path: "0.0.0", Value: "42"}})
	if !strings.Contains(r.XML, "<p:A>42</p:A>") {
		t.Errorf("edit not applied:\n%s", r.XML)
	}
	for name, req := range map[string]Request{
		"bad xml":      {XML: "<a><b></a>"},
		"bad op":       {XML: doc, Edit: &EditReq{Op: "nope", Path: ""}},
		"bad path":     {XML: doc, Edit: &EditReq{Op: "text", Path: "9.9"}},
		"malformed":    {XML: doc, Edit: &EditReq{Op: "text", Path: "x"}},
		"delete root":  {XML: doc, Edit: &EditReq{Op: "delete", Path: ""}},
		"invalid name": {XML: doc, Edit: &EditReq{Op: "name", Path: "0", Value: "a b"}},
	} {
		if r := Process(req); r.Error == "" {
			t.Errorf("%s: expected error, got %+v", name, r)
		}
	}
	if r := Process(Request{XML: " \n"}); !r.Empty {
		t.Errorf("blank input should be Empty: %+v", r)
	}
}
