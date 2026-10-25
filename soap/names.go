package soap

import (
	"strings"
	"unicode"
)

var initialisms = map[string]bool{
	"ID": true, "URL": true, "URI": true, "HTTP": true, "HTTPS": true, "API": true,
	"XML": true, "JSON": true, "SQL": true, "IP": true, "UUID": true, "UI": true,
	"TCP": true, "UDP": true, "HTML": true, "SSL": true, "TLS": true, "UTF": true,
}

// words splits an identifier on separators and camelCase boundaries.
// "TransactionID" -> [Transaction ID], "tx_id" -> [tx id]. Digits stay
// attached to the word before them.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

func title(w string) string {
	rs := []rune(w)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// GoName converts an XML name to an exported Go identifier.
// All-caps words (MSISDN) are kept as written; Id/Url/Http etc. become ID/URL/HTTP.
func GoName(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		if initialisms[strings.ToUpper(w)] {
			b.WriteString(strings.ToUpper(w))
		} else {
			b.WriteString(title(w))
		}
	}
	name := b.String()
	if name == "" {
		return "Field"
	}
	if first := []rune(name)[0]; !unicode.IsUpper(first) {
		name = "X" + name
	}
	return name
}

func SnakeCase(s string) string {
	ws := words(s)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	if len(ws) == 0 {
		return s
	}
	return strings.Join(ws, "_")
}

func CamelCase(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return s
	}
	var b strings.Builder
	for i, w := range ws {
		w = strings.ToLower(w)
		if i > 0 {
			w = title(w)
		}
		b.WriteString(w)
	}
	return b.String()
}
