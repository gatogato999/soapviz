package config

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	c, err := Parse(strings.NewReader(`
; comment
[server]
listen = 0.0.0.0:9000   ; inline comment
open_browser = yes
max_body_mb = 2

[ui]
theme = Dark

[generator]
json = true
json_case = camel
package = "pay"
root = Response # trailing
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:9000" || !c.OpenBrowser || c.MaxBodyMB != 2 || c.Theme != "dark" {
		t.Errorf("server/ui wrong: %+v", c)
	}
	if !c.Gen.JSON || c.Gen.JSONCase != "camel" || c.Gen.Package != "pay" || c.Gen.Root != "Response" {
		t.Errorf("generator wrong: %+v", c.Gen)
	}
}

func TestDefaults(t *testing.T) {
	c, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if c != Default() || c.OpenBrowser || c.Theme != "dim" {
		t.Errorf("empty file must equal defaults: %+v", c)
	}
}

func TestParseErrors(t *testing.T) {
	for in, want := range map[string]string{
		"[server]\nlisten":                "line 2",
		"[server]\nopen_browser = maybe":  "boolean",
		"[ui]\ntheme = neon":              "one of",
		"[server]\nlistn = x":             "unknown setting",
		"[generator]\npackage = 1bad":     "valid Go package",
		"[server\nlisten = x":             "unterminated",
		"[server]\nmax_body_mb = 0":       "at least 1",
		"[generator]\njson_case = Pascal": "one of",
		"orphan = 1":                      "unknown setting",
	} {
		_, err := Parse(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want error containing %q", in, err, want)
		}
	}
}
