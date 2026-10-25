package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"soapviz/config"
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

func TestIndexUsesConfiguredTheme(t *testing.T) {
	cfg := config.Default()
	cfg.Theme = "dark"
	ts := newTestServer(t, cfg)
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(b.String(), `data-theme="dark"`) {
		t.Errorf("theme not rendered into page:\n%s", b.String()[:200])
	}
}

func TestClientConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Gen.JSON, cfg.Gen.JSONCase, cfg.Gen.Package = true, "camel", "pay"
	ts := newTestServer(t, cfg)
	res, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got struct {
		Theme string `json:"theme"`
		Gen   struct {
			JSON     bool   `json:"json"`
			JSONCase string `json:"jsonCase"`
			Pkg      string `json:"pkg"`
		} `json:"gen"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Theme != "dim" || !got.Gen.JSON || got.Gen.JSONCase != "camel" || got.Gen.Pkg != "pay" {
		t.Errorf("unexpected config: %+v", got)
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
