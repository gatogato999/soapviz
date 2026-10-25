package soap

import "strings"

var (
	textEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;",
		"\n", "&#xA;", "\r", "&#xD;", "\t", "&#x9;")
)

// Render writes the tree as indented XML (two spaces, trailing newline).
func Render(n *Node) string {
	var b strings.Builder
	render(&b, n, 0)
	return b.String()
}

func render(b *strings.Builder, n *Node, depth int) {
	ind := strings.Repeat("  ", depth)
	q := n.QName()
	b.WriteString(ind + "<" + q)
	for _, a := range n.Attrs {
		b.WriteString(" " + a.QName() + `="` + attrEsc.Replace(a.Value) + `"`)
	}
	switch {
	case len(n.Children) == 0 && n.Text == "":
		b.WriteString("/>\n")
	case len(n.Children) == 0:
		b.WriteString(">" + textEsc.Replace(n.Text) + "</" + q + ">\n")
	default:
		b.WriteString(">\n")
		if n.Text != "" {
			b.WriteString(ind + "  " + textEsc.Replace(n.Text) + "\n")
		}
		for _, c := range n.Children {
			render(b, c, depth+1)
		}
		b.WriteString(ind + "</" + q + ">\n")
	}
}
