package soap

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

const xmlNS = "http://www.w3.org/XML/1998/namespace"

// Parse reads one XML document into a Node tree. Comments, processing
// instructions and directives are dropped. Whitespace around text is trimmed.
func Parse(r io.Reader) (*Node, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	dec := xml.NewDecoder(bytes.NewReader(data))
	// Input is already decoded text, so any declared encoding is ignored.
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }

	var (
		root   *Node
		stack  []*Node
		scopes []map[string]string // prefix -> URI, parallel to stack
	)
	resolve := func(prefix string, isAttr bool) string {
		if isAttr && prefix == "" {
			return "" // unprefixed attributes have no namespace
		}
		if prefix == "xml" {
			return xmlNS
		}
		for i := len(scopes) - 1; i >= 0; i-- {
			if u, ok := scopes[i][prefix]; ok {
				return u
			}
		}
		return ""
	}
	fail := func(format string, a ...any) error {
		line, col := dec.InputPos()
		return fmt.Errorf("line %d, col %d: %s", line, col, fmt.Sprintf(format, a...))
	}

	for {
		// RawToken keeps prefixes as written; we resolve them ourselves.
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 && root != nil {
				return nil, fail("multiple root elements")
			}
			scope := map[string]string{}
			attrs := make([]Attr, 0, len(t.Attr))
			for _, a := range t.Attr {
				attrs = append(attrs, Attr{Name: a.Name.Local, Prefix: a.Name.Space, Value: a.Value})
				if a.Name.Space == "xmlns" {
					scope[a.Name.Local] = a.Value
				} else if a.Name.Space == "" && a.Name.Local == "xmlns" {
					scope[""] = a.Value
				}
			}
			scopes = append(scopes, scope)
			n := &Node{
				Name:   t.Name.Local,
				Prefix: t.Name.Space,
				Space:  resolve(t.Name.Space, false),
				Attrs:  attrs,
			}
			for i := range n.Attrs {
				if !n.Attrs[i].IsNS() {
					n.Attrs[i].Space = resolve(n.Attrs[i].Prefix, true)
				}
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			} else {
				root = n
			}
			stack = append(stack, n)

		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fail("unexpected </%s>", qname(t.Name.Space, t.Name.Local))
			}
			top := stack[len(stack)-1]
			if top.Name != t.Name.Local || top.Prefix != t.Name.Space {
				return nil, fail("expected </%s>, got </%s>", top.QName(), qname(t.Name.Space, t.Name.Local))
			}
			top.Text = strings.TrimSpace(top.Text)
			stack = stack[:len(stack)-1]
			scopes = scopes[:len(scopes)-1]

		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("unexpected end of input: <%s> is not closed", stack[len(stack)-1].QName())
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	return root, nil
}

// Payload strips Envelope/Body and returns the first element inside Body.
// If root is not a SOAP Envelope it is returned unchanged.
func Payload(root *Node) *Node {
	if root.Name != "Envelope" {
		return root
	}
	for _, c := range root.Children {
		if c.Name == "Body" {
			if len(c.Children) > 0 {
				return c.Children[0]
			}
			return c
		}
	}
	return root
}
