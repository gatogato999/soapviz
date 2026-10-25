package web

import (
	"go/scanner"
	"go/token"
	"html/template"
	"strings"
)

var predeclared = map[string]bool{
	"string": true, "bool": true, "byte": true, "rune": true, "error": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
}

// highlightGo wraps Go tokens in <span class="..."> using go/scanner. The text
// is HTML-escaped; the original source is reproduced exactly between spans.
func highlightGo(src string) template.HTML {
	var s scanner.Scanner
	file := token.NewFileSet().AddFile("", -1, len(src))
	s.Init(file, []byte(src), func(token.Position, string) {}, scanner.ScanComments)

	var b strings.Builder
	last := 0
	afterType := false // identifier right after the "type" keyword
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue // inserted by the scanner, not in the source
		}
		text := lit
		if text == "" {
			text = tok.String()
		}
		off := file.Offset(pos)
		if off < last || off+len(text) > len(src) {
			continue
		}
		b.WriteString(template.HTMLEscapeString(src[last:off]))

		class := ""
		switch {
		case tok.IsKeyword():
			class = "kw"
		case tok == token.STRING || tok == token.CHAR:
			class = "str"
		case tok == token.INT || tok == token.FLOAT || tok == token.IMAG:
			class = "num"
		case tok == token.COMMENT:
			class = "com"
		case tok == token.IDENT && (afterType || predeclared[lit]):
			class = "typ"
		}
		afterType = tok == token.TYPE

		chunk := template.HTMLEscapeString(src[off : off+len(text)])
		if class != "" {
			b.WriteString(`<span class="` + class + `">` + chunk + `</span>`)
		} else {
			b.WriteString(chunk)
		}
		last = off + len(text)
	}
	b.WriteString(template.HTMLEscapeString(src[last:]))
	return template.HTML(b.String())
}
