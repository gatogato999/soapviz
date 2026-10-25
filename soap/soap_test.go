package soap

import (
	"strings"
	"testing"
)

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:pay="http://example.com/pay">
  <soapenv:Header/>
  <soapenv:Body>
    <pay:GetStatementResponse status="ok">
      <pay:TransactionID>T-001</pay:TransactionID>
      <pay:Account currency="SDG"><pay:MSISDN>0912345678</pay:MSISDN></pay:Account>
      <pay:Item seq="1"><pay:Amount>10.50</pay:Amount><pay:Memo>a &amp; b</pay:Memo></pay:Item>
      <pay:Item seq="2"><pay:Amount>3</pay:Amount></pay:Item>
      <pay:Item><pay:Amount>7</pay:Amount></pay:Item>
    </pay:GetStatementResponse>
  </soapenv:Body>
</soapenv:Envelope>`

func mustParse(t *testing.T, s string) *Node {
	t.Helper()
	n, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestParsePayload(t *testing.T) {
	root := mustParse(t, sample)
	if root.Name != "Envelope" || root.Space != "http://schemas.xmlsoap.org/soap/envelope/" {
		t.Fatalf("bad root: %+v", root)
	}
	p := Payload(root)
	if p.Name != "GetStatementResponse" || p.Space != "http://example.com/pay" || len(p.Children) != 5 {
		t.Fatalf("bad payload: %+v", p)
	}
	if got := p.Children[2].Children[1].Text; got != "a & b" {
		t.Fatalf("entity not decoded: %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "<a><b></a>", "<a/><b/>", "<a>"} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func TestRenderRoundTrip(t *testing.T) {
	root := mustParse(t, sample)
	out := Render(root)
	again := Render(mustParse(t, out))
	if out != again {
		t.Fatalf("render not stable:\n%s\n---\n%s", out, again)
	}
	if !strings.Contains(out, "a &amp; b") || !strings.Contains(out, "<soapenv:Header/>") {
		t.Fatalf("unexpected render:\n%s", out)
	}
}

func TestEdit(t *testing.T) {
	root := mustParse(t, sample)
	// Envelope > Body(1) > Response(0) > Item(2)
	path := []int{1, 0, 2}
	if err := root.Apply(Edit{Op: "text", Path: []int{1, 0, 0}, Value: "T-999"}); err != nil {
		t.Fatal(err)
	}
	if err := root.Apply(Edit{Op: "attr", Path: path, Index: 0, Value: "9"}); err != nil {
		t.Fatal(err)
	}
	if err := root.Apply(Edit{Op: "dup", Path: path}); err != nil {
		t.Fatal(err)
	}
	if err := root.Apply(Edit{Op: "delete", Path: []int{1, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := root.Apply(Edit{Op: "name", Path: []int{1, 0, 0}, Value: "pay:Tx"}); err != nil {
		t.Fatal(err)
	}
	if err := root.Apply(Edit{Op: "name", Path: []int{1, 0, 0}, Value: "bad name"}); err == nil {
		t.Fatal("expected invalid name error")
	}
	if err := root.Apply(Edit{Op: "delete", Path: nil}); err == nil {
		t.Fatal("expected error deleting root")
	}
	out := Render(root)
	for _, want := range []string{"<pay:Tx>T-999</pay:Tx>", `seq="9"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "<pay:Item") != 4 || strings.Contains(out, "<pay:Account") {
		t.Errorf("dup/delete wrong:\n%s", out)
	}
}

func TestNames(t *testing.T) {
	cases := []struct{ in, goN, snake, camel string }{
		{"TransactionID", "TransactionID", "transaction_id", "transactionId"},
		{"transaction_id", "TransactionID", "transaction_id", "transactionId"},
		{"MSISDN", "MSISDN", "msisdn", "msisdn"},
		{"user-name", "UserName", "user_name", "userName"},
		{"XMLHttpRequest", "XMLHTTPRequest", "xml_http_request", "xmlHttpRequest"},
		{"2fa", "X2fa", "2fa", "2fa"},
		{"Address2", "Address2", "address2", "address2"},
	}
	for _, c := range cases {
		if g := GoName(c.in); g != c.goN {
			t.Errorf("GoName(%q) = %q, want %q", c.in, g, c.goN)
		}
		if g := SnakeCase(c.in); g != c.snake {
			t.Errorf("SnakeCase(%q) = %q, want %q", c.in, g, c.snake)
		}
		if g := CamelCase(c.in); g != c.camel {
			t.Errorf("CamelCase(%q) = %q, want %q", c.in, g, c.camel)
		}
	}
}

func TestGenerate(t *testing.T) {
	p := Payload(mustParse(t, sample))
	raw, err := Generate(p, Options{JSON: true, JSONCase: CaseSnake, OmitEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	got := squash(raw) // gofmt aligns columns; compare with single spaces
	for _, want := range []string{
		"type GetStatementResponse struct { XMLName xml.Name",
		"`xml:\"GetStatementResponse\" json:\"-\"`",
		"Status string `xml:\"status,attr\" json:\"status\"`",
		"TransactionID string `xml:\"TransactionID\" json:\"transaction_id\"`",
		"Account Account `xml:\"Account\" json:\"account\"`",
		"Item []Item `xml:\"Item\" json:\"item,omitempty\"`",
		"Seq string `xml:\"seq,attr,omitempty\" json:\"seq,omitempty\"`",
		"Memo string `xml:\"Memo,omitempty\" json:\"memo,omitempty\"`",
		"Currency string `xml:\"currency,attr\" json:\"currency\"`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(raw, "type Item struct") != 1 {
		t.Errorf("Item should be merged into one type:\n%s", raw)
	}
	if strings.HasPrefix(raw, "package") {
		t.Errorf("unexpected package clause:\n%s", raw)
	}

	ns, err := Generate(p, Options{Namespaces: true, Package: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ns, "package pay") || !strings.Contains(ns, `xml:"http://example.com/pay GetStatementResponse"`) {
		t.Errorf("namespace/package output wrong:\n%s", ns)
	}
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestGenerateNameCollision(t *testing.T) {
	n := mustParse(t, `<R><Sender><Address><City>a</City></Address></Sender><Receiver><Address><Zip>1</Zip></Address></Receiver></R>`)
	raw, err := Generate(n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := squash(raw)
	for _, want := range []string{"type Address struct", "type ReceiverAddress struct", "Address ReceiverAddress"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateLeafWithAttr(t *testing.T) {
	n := mustParse(t, `<R><Amt cur="SDG">5</Amt></R>`)
	raw, err := Generate(n, Options{JSON: true, JSONCase: CaseCamel})
	if err != nil {
		t.Fatal(err)
	}
	got := squash(raw)
	for _, want := range []string{
		"type Amt struct { Cur string `xml:\"cur,attr\" json:\"cur\"` Value string `xml:\",chardata\" json:\"value\"` }",
		"Amt Amt `xml:\"Amt\" json:\"amt\"`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, raw)
		}
	}
}
