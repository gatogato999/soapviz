package soap

import (
	"fmt"
	"strconv"
	"strings"
)

// Node is one XML element. Children are ordered; order matters for XML and for
// detecting repeated elements in the generator.
type Node struct {
	Name     string // local name
	Prefix   string // prefix as written in the source ("" if none)
	Space    string // resolved namespace URI ("" if none)
	Attrs    []Attr
	Text     string // trimmed character data
	Children []*Node
}

type Attr struct {
	Name   string // local name
	Prefix string // prefix as written ("xmlns" for xmlns:foo declarations)
	Space  string // resolved namespace URI ("" if none)
	Value  string
}

// IsNS reports whether the attribute is a namespace declaration.
func (a Attr) IsNS() bool {
	return a.Prefix == "xmlns" || (a.Prefix == "" && a.Name == "xmlns")
}

func (a Attr) QName() string  { return qname(a.Prefix, a.Name) }
func (n *Node) QName() string { return qname(n.Prefix, n.Name) }

func qname(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + ":" + name
}

func (n *Node) Clone() *Node {
	c := *n
	c.Attrs = append([]Attr(nil), n.Attrs...)
	c.Children = make([]*Node, len(n.Children))
	for i, ch := range n.Children {
		c.Children[i] = ch.Clone()
	}
	return &c
}

// At returns the node at the given child-index path, or nil.
func (n *Node) At(path []int) *Node {
	cur := n
	for _, i := range path {
		if i < 0 || i >= len(cur.Children) {
			return nil
		}
		cur = cur.Children[i]
	}
	return cur
}

// ParsePath parses "0.2.1" into child indexes. "" is the root.
func ParsePath(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return nil, fmt.Errorf("bad path %q", s)
		}
		out[i] = v
	}
	return out, nil
}

func FormatPath(p []int) string {
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ".")
}
