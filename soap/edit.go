package soap

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Edit is one change to a tree. Path addresses the target node.
type Edit struct {
	Op    string // text | attr | name | delete | dup
	Path  []int
	Index int // attribute index, for Op "attr"
	Value string
}

func (root *Node) Apply(e Edit) error {
	target := root.At(e.Path)
	if target == nil {
		return fmt.Errorf("no node at path %q", FormatPath(e.Path))
	}
	switch e.Op {
	case "text":
		target.Text = strings.TrimSpace(e.Value)
	case "attr":
		if e.Index < 0 || e.Index >= len(target.Attrs) {
			return errors.New("no such attribute")
		}
		target.Attrs[e.Index].Value = e.Value
	case "name":
		prefix, name := "", e.Value
		if i := strings.IndexByte(e.Value, ':'); i >= 0 {
			prefix, name = e.Value[:i], e.Value[i+1:]
		}
		if !validName(name) || (prefix != "" && !validName(prefix)) {
			return fmt.Errorf("%q is not a valid element name", e.Value)
		}
		target.Prefix, target.Name = prefix, name
	case "delete", "dup":
		if len(e.Path) == 0 {
			return fmt.Errorf("cannot %s the root element", e.Op)
		}
		parent := root.At(e.Path[:len(e.Path)-1])
		i := e.Path[len(e.Path)-1]
		if e.Op == "delete" {
			parent.Children = slices.Delete(parent.Children, i, i+1)
		} else {
			parent.Children = slices.Insert(parent.Children, i+1, target.Clone())
		}
	default:
		return fmt.Errorf("unknown op %q", e.Op)
	}
	return nil
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case unicode.IsLetter(r) || r == '_':
		case i > 0 && (unicode.IsDigit(r) || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}
