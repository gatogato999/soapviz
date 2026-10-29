// Package config reads config.ini: server settings and the defaults that a
// browser falls back to when the developer has not saved their own.
package config

import (
	"bufio"
	"fmt"
	"go/token"
	"io"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen      string
	OpenBrowser bool
	MaxBodyMB   int
	Theme       string // light | dim | dark
	Gen         Generator
}

// Generator holds the default state of the Go output options.
type Generator struct {
	Envelope   bool   `json:"envelope"`
	Namespaces bool   `json:"namespaces"`
	JSON       bool   `json:"json"`
	JSONCase   string `json:"jsonCase"` // none | snake | camel
	OmitEmpty  bool   `json:"omitEmpty"`
	Inline     bool   `json:"inline"`
	Root       string `json:"root"`
	Package    string `json:"pkg"`
}

var Themes = []string{"light", "dim", "dark"}

func Default() Config {
	return Config{
		Listen:    "127.0.0.1:8484",
		MaxBodyMB: 8,
		Theme:     "dim",
		Gen:       Generator{JSONCase: "snake"},
	}
}

// Load reads path. A missing file is reported with an error satisfying
// errors.Is(err, fs.ErrNotExist).
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Default(), err
	}
	defer f.Close()
	c, err := Parse(f)
	if err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse reads INI text: [section] headers, key = value lines, and comments
// starting with ; or # (whole line, or after whitespace). Unknown keys are
// errors so typos do not silently fall back to defaults.
func Parse(r io.Reader) (Config, error) {
	c := Default()
	section := ""
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") {
				return c, fmt.Errorf("line %d: unterminated section header", n)
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("line %d: expected key = value", n)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = unquote(strings.TrimSpace(val))
		if err := c.set(section, key, val); err != nil {
			return c, fmt.Errorf("line %d: %w", n, err)
		}
	}
	return c, sc.Err()
}

func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		if (s[i] == ';' || s[i] == '#') && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func (c *Config) set(section, key, val string) error {
	var err error
	switch section + "." + key {
	case "server.listen":
		if val == "" {
			return fmt.Errorf("server.listen must not be empty")
		}
		c.Listen = val
	case "server.open_browser":
		c.OpenBrowser, err = parseBool(val)
	case "server.max_body_mb":
		c.MaxBodyMB, err = strconv.Atoi(val)
		if err == nil && c.MaxBodyMB < 1 {
			err = fmt.Errorf("must be at least 1")
		}
	case "ui.theme":
		c.Theme, err = oneOf(val, Themes...)
	case "generator.include_envelope":
		c.Gen.Envelope, err = parseBool(val)
	case "generator.namespaces":
		c.Gen.Namespaces, err = parseBool(val)
	case "generator.json":
		c.Gen.JSON, err = parseBool(val)
	case "generator.json_case":
		c.Gen.JSONCase, err = oneOf(val, "none", "snake", "camel")
	case "generator.omit_empty":
		c.Gen.OmitEmpty, err = parseBool(val)
	case "generator.inline":
		c.Gen.Inline, err = parseBool(val)
	case "generator.root":
		c.Gen.Root = val
	case "generator.package":
		if val != "" && !token.IsIdentifier(val) {
			return fmt.Errorf("generator.package: %q is not a valid Go package name", val)
		}
		c.Gen.Package = val
	default:
		return fmt.Errorf("unknown setting [%s] %s", section, key)
	}
	if err != nil {
		return fmt.Errorf("%s.%s: %w", section, key, err)
	}
	return nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true, nil
	case "false", "no", "off", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q is not a boolean (use true/false)", s)
}

func oneOf(s string, options ...string) (string, error) {
	s = strings.ToLower(s)
	for _, o := range options {
		if s == o {
			return s, nil
		}
	}
	return "", fmt.Errorf("%q is not one of: %s", s, strings.Join(options, ", "))
}
