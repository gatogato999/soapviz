package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"soapviz/config"
	"soapviz/engine"
)

func newTestServer(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func TestIndexUsesConfiguredTheme(t *testing.T) {
	cfg := config.Default()
	cfg.Theme = "dark"
	body := get(t, newTestServer(t, cfg).URL+"/")
	if !strings.Contains(body, `data-theme="dark"`) {
		t.Errorf("theme not rendered into page:\n%.200s", body)
	}
	// Relative asset paths keep the page working under a sub-path (GitHub Pages).
	if strings.Contains(body, `"/static/`) {
		t.Errorf("index.html must use relative asset paths")
	}
}

func TestClientConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Gen.JSON, cfg.Gen.JSONCase, cfg.Gen.Package = true, "camel", "pay"
	var got struct {
		Theme string `json:"theme"`
		Gen   struct {
			JSON     bool   `json:"json"`
			JSONCase string `json:"jsonCase"`
			Pkg      string `json:"pkg"`
		} `json:"gen"`
	}
	if err := json.Unmarshal([]byte(get(t, newTestServer(t, cfg).URL+"/api/config")), &got); err != nil {
		t.Fatal(err)
	}
	if got.Theme != "dim" || !got.Gen.JSON || got.Gen.JSONCase != "camel" || got.Gen.Pkg != "pay" {
		t.Errorf("unexpected config: %+v", got)
	}
}

func TestProcess(t *testing.T) {
	ts := newTestServer(t, config.Default())
	res, err := http.Post(ts.URL+"/api/process", "application/json",
		strings.NewReader(`{"xml":"<a><b>1</b></a>","gen":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var r engine.Response
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Tree == nil || r.Tree.Local != "a" || !strings.Contains(r.Code, "type A struct") {
		t.Errorf("unexpected response: %+v", r)
	}
}

func TestProcessBodyLimit(t *testing.T) {
	cfg := config.Default()
	cfg.MaxBodyMB = 1
	ts := newTestServer(t, cfg)
	big := `{"xml":"` + strings.Repeat("a", 2<<20) + `"}`
	res, err := http.Post(ts.URL+"/api/process", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}
