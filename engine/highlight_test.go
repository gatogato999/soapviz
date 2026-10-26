package engine

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

func TestHighlightGoRoundTrip(t *testing.T) {
	src := "type Item struct {\n\tSeq string `xml:\"seq,attr\"` // <tag> & more\n\tN   []int\n}\n"
	out := highlightGo(src)
	plain := html.UnescapeString(regexp.MustCompile(`</?span[^>]*>`).ReplaceAllString(out, ""))
	if plain != src {
		t.Fatalf("highlighting changed the text:\n%q\n%q", plain, src)
	}
	for _, want := range []string{`<span class="kw">type</span>`, `<span class="typ">Item</span>`, `<span class="typ">string</span>`, `<span class="str">`, `<span class="com">`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}
