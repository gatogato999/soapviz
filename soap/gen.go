package soap

import (
	"errors"
	"fmt"
	"go/format"
	"strconv"
	"strings"
)

type Case int

const (
	CaseNone  Case = iota // json key = XML name as written
	CaseSnake             // transaction_id
	CaseCamel             // transactionId
)

type Options struct {
	Package    string // if set, emit "package X" and the encoding/xml import
	Root       string // override root type name
	Namespaces bool   // put namespace URIs in xml tags ("uri name")
	JSON       bool   // add json tags
	JSONCase   Case
	OmitEmpty  bool // json omitempty on optional and repeated fields
}

// Generate builds Go struct source from a sample element. Elements with the
// same name under the same parent are merged, so repeated and optional
// children are detected across all occurrences.
func Generate(n *Node, opt Options) (string, error) {
	if n == nil {
		return "", errors.New("nothing to generate from")
	}
	rootName := GoName(n.Name)
	if opt.Root != "" {
		rootName = GoName(opt.Root)
	}
	g := &generator{opt: opt, names: map[string]string{rootName: "\x00root"}}
	g.resolve(buildShape([]*Node{n}), rootName, "", true)

	var b strings.Builder
	if opt.Package != "" {
		b.WriteString("package " + opt.Package + "\n\nimport \"encoding/xml\"\n\n")
	} else {
		b.WriteString("package p\n\n") // placeholder so gofmt accepts it
	}
	for _, d := range g.defs {
		if d.skip {
			continue
		}
		fmt.Fprintf(&b, "type %s struct {\n%s\n}\n\n", d.name, d.body)
	}
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return "", fmt.Errorf("gofmt: %w", err)
	}
	return strings.TrimPrefix(string(out), "package p\n\n"), nil
}

// --- shape: all occurrences of one element position, merged ---

type attrInfo struct {
	name, space string
	optional    bool
}

type fieldInfo struct {
	name, space        string
	shape              *shape
	repeated, optional bool
}

type shape struct {
	name, space string
	attrs       []*attrInfo
	fields      []*fieldInfo
	hasText     bool
}

func buildShape(insts []*Node) *shape {
	s := &shape{name: insts[0].Name, space: insts[0].Space}
	var (
		attrs        = map[string]*attrInfo{}
		attrPresent  = map[string]int{}
		attrOrder    []string
		fields       = map[string]*fieldInfo{}
		fieldPresent = map[string]int{}
		fieldOrder   []string
		childInsts   = map[string][]*Node{}
	)
	for _, in := range insts {
		if in.Text != "" {
			s.hasText = true
		}
		for _, a := range in.Attrs {
			if a.IsNS() {
				continue
			}
			k := a.Space + "\x00" + a.Name
			if _, ok := attrs[k]; !ok {
				attrs[k] = &attrInfo{name: a.Name, space: a.Space}
				attrOrder = append(attrOrder, k)
			}
			attrPresent[k]++
		}
		counts := map[string]int{}
		for _, c := range in.Children {
			k := c.Space + "\x00" + c.Name
			if _, ok := fields[k]; !ok {
				fields[k] = &fieldInfo{name: c.Name, space: c.Space}
				fieldOrder = append(fieldOrder, k)
			}
			counts[k]++
			childInsts[k] = append(childInsts[k], c)
		}
		for k, n := range counts {
			fieldPresent[k]++
			if n > 1 {
				fields[k].repeated = true
			}
		}
	}
	for _, k := range attrOrder {
		a := attrs[k]
		a.optional = attrPresent[k] < len(insts)
		s.attrs = append(s.attrs, a)
	}
	for _, k := range fieldOrder {
		f := fields[k]
		f.optional = fieldPresent[k] < len(insts)
		f.shape = buildShape(childInsts[k])
		s.fields = append(s.fields, f)
	}
	return s
}

// --- emitting ---

type def struct {
	name, body string
	skip       bool // identical to an earlier type of the same name
}

type generator struct {
	opt   Options
	defs  []*def            // in pre-order, so the root comes first
	names map[string]string // type name -> body, to reuse or disambiguate
}

// fieldSet keeps Go field names and JSON keys unique inside one struct.
type fieldSet struct{ goUsed, jsonUsed map[string]bool }

func (f *fieldSet) goName(n string) string  { return unique(f.goUsed, n) }
func (f *fieldSet) jsonKey(n string) string { return unique(f.jsonUsed, n) }

func unique(used map[string]bool, n string) string {
	c := n
	for i := 2; used[c]; i++ {
		c = n + strconv.Itoa(i)
	}
	used[c] = true
	return c
}

func (g *generator) jsonName(raw string) string {
	switch g.opt.JSONCase {
	case CaseSnake:
		return SnakeCase(raw)
	case CaseCamel:
		return CamelCase(raw)
	}
	return raw
}

func (g *generator) tags(f *fieldSet, xmlTag, rawName string, omitJSON bool) string {
	t := "xml:" + strconv.Quote(xmlTag)
	if g.opt.JSON {
		j := f.jsonKey(g.jsonName(rawName))
		if omitJSON {
			j += ",omitempty"
		}
		t += " json:" + strconv.Quote(j)
	}
	return "`" + t + "`"
}

func (g *generator) qualified(name, space string) string {
	if g.opt.Namespaces && space != "" {
		return space + " " + name
	}
	return name
}

// resolve returns the Go type for s, emitting struct definitions as needed.
// Leaf elements with no attributes are plain strings.
func (g *generator) resolve(s *shape, base, parent string, root bool) string {
	if !root && len(s.fields) == 0 && len(s.attrs) == 0 {
		return "string"
	}
	slot := &def{}
	g.defs = append(g.defs, slot)

	f := &fieldSet{goUsed: map[string]bool{}, jsonUsed: map[string]bool{}}
	var lines []string

	if root {
		name := f.goName("XMLName")
		t := "xml:" + strconv.Quote(g.qualified(s.name, s.space))
		if g.opt.JSON {
			t += ` json:"-"`
		}
		lines = append(lines, fmt.Sprintf("%s xml.Name `%s`", name, t))
	}
	for _, a := range s.attrs {
		xt := g.qualified(a.name, a.space) + ",attr"
		if a.optional {
			xt += ",omitempty"
		}
		lines = append(lines, fmt.Sprintf("%s string %s",
			f.goName(GoName(a.name)), g.tags(f, xt, a.name, a.optional && g.opt.OmitEmpty)))
	}
	if len(s.fields) == 0 || s.hasText {
		name := "Value"
		if len(s.fields) > 0 {
			name = "Text" // mixed content
		}
		t := "`xml:\",chardata\"`"
		if g.opt.JSON {
			t = "`xml:\",chardata\" json:" + strconv.Quote(strings.ToLower(name)) + "`"
		}
		lines = append(lines, fmt.Sprintf("%s string %s", f.goName(name), t))
	}
	for _, c := range s.fields {
		childBase := GoName(c.name)
		typ := g.resolve(c.shape, childBase, base, false)
		xt := g.qualified(c.name, c.space)
		goType := typ
		switch {
		case c.repeated:
			goType = "[]" + typ
		case c.optional && typ != "string":
			goType = "*" + typ
			xt += ",omitempty"
		case c.optional:
			xt += ",omitempty"
		}
		omitJSON := g.opt.OmitEmpty && (c.optional || c.repeated)
		lines = append(lines, fmt.Sprintf("%s %s %s",
			f.goName(childBase), goType, g.tags(f, xt, c.name, omitJSON)))
	}

	body := strings.Join(lines, "\n")
	if root {
		slot.name, slot.body = base, body
		g.names[base] = body
		return base
	}
	name, reused := g.pick(base, parent, body)
	if reused {
		slot.skip = true
	} else {
		slot.name, slot.body = name, body
	}
	return name
}

// pick chooses a type name: the plain name, then Parent+Name, then numbered.
// A name already taken by an identical body is reused.
func (g *generator) pick(base, parent, body string) (name string, reused bool) {
	try := func(c string) (done, reuse bool) {
		sig, taken := g.names[c]
		if !taken {
			g.names[c] = body
			return true, false
		}
		return sig == body, sig == body
	}
	for _, c := range []string{base, parent + base} {
		if done, reuse := try(c); done {
			return c, reuse
		}
	}
	for i := 2; ; i++ {
		c := parent + base + strconv.Itoa(i)
		if done, reuse := try(c); done {
			return c, reuse
		}
	}
}
