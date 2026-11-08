package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gatogato999/soapviz/config"
	"github.com/gatogato999/soapviz/engine"
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

// --- team samples ---

func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func sharedServer(t *testing.T, write bool) (*httptest.Server, string) {
	t.Helper()
	cfg := config.Default()
	cfg.SharedDir = t.TempDir()
	cfg.SharedWrite = write
	return newTestServer(t, cfg), cfg.SharedDir
}

func TestSharedDisabled(t *testing.T) {
	ts := newTestServer(t, config.Default())
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/shared"}, {"GET", "/api/shared/sample?name=x"},
		{"PUT", "/api/shared/sample?name=x"}, {"DELETE", "/api/shared/sample?name=x"},
	} {
		if code, _ := do(t, c.method, ts.URL+c.path, `{"xml":"<a/>"}`); code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, code)
		}
	}
	var cfg struct {
		Shared struct{ Enabled, Writable bool } `json:"shared"`
		Tags   map[string]string                `json:"tags"`
	}
	if err := json.Unmarshal([]byte(get(t, ts.URL+"/api/config")), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Shared.Enabled || cfg.Shared.Writable || cfg.Tags["req"] != "yellow" || cfg.Tags["res"] != "blue" {
		t.Errorf("config: %+v", cfg)
	}
}

func TestSharedReadOnly(t *testing.T) {
	ts, dir := sharedServer(t, false)
	if err := os.WriteFile(filepath.Join(dir, "GetStatement [Req].xml"), []byte("<a>1</a>"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, m := do(t, "GET", ts.URL+"/api/shared", "")
	list, _ := m["samples"].([]any)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["tag"] != "Req" {
		t.Fatalf("list: %d %v", code, m)
	}
	code, m = do(t, "GET", ts.URL+"/api/shared/sample?name=getstatement", "")
	if code != 200 || m["xml"] != "<a>1</a>" || m["tag"] != "Req" {
		t.Fatalf("get: %d %v", code, m)
	}
	if code, _ := do(t, "PUT", ts.URL+"/api/shared/sample?name=New", `{"xml":"<a/>"}`); code != http.StatusForbidden {
		t.Errorf("PUT on read-only = %d, want 403", code)
	}
	if code, _ := do(t, "DELETE", ts.URL+"/api/shared/sample?name=GetStatement", ""); code != http.StatusForbidden {
		t.Errorf("DELETE on read-only = %d, want 403", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "GetStatement [Req].xml")); err != nil {
		t.Errorf("file must survive: %v", err)
	}
	if code, _ := do(t, "GET", ts.URL+"/api/shared/sample?name=missing", ""); code != http.StatusNotFound {
		t.Errorf("missing = %d", code)
	}
	var cfg struct {
		Shared struct{ Enabled, Writable bool }
	}
	json.Unmarshal([]byte(get(t, ts.URL+"/api/config")), &cfg)
	if !cfg.Shared.Enabled || cfg.Shared.Writable {
		t.Errorf("config should say enabled, read-only: %+v", cfg)
	}
}

func TestSharedWriteFlow(t *testing.T) {
	ts, dir := sharedServer(t, true)
	u := ts.URL + "/api/shared/sample?name=Req%20One"

	code, m := do(t, "PUT", u, `{"tag":"Req","xml":"<a>1</a>"}`)
	if code != 200 || m["tag"] != "Req" {
		t.Fatalf("create: %d %v", code, m)
	}
	v1 := int64(m["updatedAt"].(float64))
	if _, err := os.Stat(filepath.Join(dir, "Req One [Req].xml")); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	code, m = do(t, "PUT", u, `{"tag":"Req","xml":"<a>2</a>"}`)
	if code != 409 || m["code"] != "exists" {
		t.Errorf("duplicate create: %d %v", code, m)
	}
	code, m = do(t, "PUT", u, `{"tag":"Req","xml":"<a>2</a>","ifUpdatedAt":1}`)
	if code != 409 || m["code"] != "conflict" || int64(m["updatedAt"].(float64)) != v1 {
		t.Errorf("stale write: %d %v", code, m)
	}
	code, _ = do(t, "PUT", u, `{"tag":"Res","xml":"<a>2</a>","ifUpdatedAt":`+strconv.FormatInt(v1, 10)+`}`)
	if code != 200 {
		t.Errorf("good overwrite: %d", code)
	}
	if code, m := do(t, "GET", u, ""); code != 200 || m["xml"] != "<a>2</a>" || m["tag"] != "Res" {
		t.Errorf("after overwrite: %d %v", code, m)
	}
	if code, _ := do(t, "DELETE", u, ""); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("dir should be empty: %v", des)
	}
}

func TestSharedRejectsBadInput(t *testing.T) {
	ts, dir := sharedServer(t, true)
	for _, name := range []string{"../escape", "a%2Fb", "..", ".hidden", "a%5Cb", "x%00y", "", "has[brackets]", "sp%20"} {
		code, m := do(t, "PUT", ts.URL+"/api/shared/sample?name="+name, `{"xml":"<a/>"}`)
		if code != http.StatusBadRequest {
			t.Errorf("name %q: %d %v, want 400", name, code, m)
		}
	}
	if code, _ := do(t, "PUT", ts.URL+"/api/shared/sample?name=ok", `{"tag":"toolong","xml":"<a/>"}`); code != 400 {
		t.Errorf("bad tag: %d", code)
	}
	if code, _ := do(t, "PUT", ts.URL+"/api/shared/sample?name=ok", `{"xml":"   "}`); code != 400 {
		t.Errorf("empty xml: %d", code)
	}
	if code, _ := do(t, "PUT", ts.URL+"/api/shared/sample?name=ok", `not json`); code != 400 {
		t.Errorf("bad json: %d", code)
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("rejected requests left files: %v", des)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.xml")); !os.IsNotExist(err) {
		t.Error("path traversal escaped the folder")
	}
}

// A web page on another site cannot write team samples: PUT and DELETE need a
// CORS preflight, and this server answers it without granting anything.
func TestSharedNoCORSGrant(t *testing.T) {
	ts, _ := sharedServer(t, true)
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/shared/sample?name=x", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if h := res.Header.Get("Access-Control-Allow-Origin"); h != "" {
		t.Errorf("server must not grant cross-origin access, got %q", h)
	}
	if res.StatusCode < 400 {
		t.Errorf("preflight should be refused, got %d", res.StatusCode)
	}
}

func TestOpenSharedDirErrors(t *testing.T) {
	cfg := config.Default()
	cfg.SharedDir = filepath.Join(t.TempDir(), "missing")
	if _, err := New(cfg); err == nil {
		t.Error("read-only server must refuse a missing shared_dir")
	}
	cfg.SharedWrite = true
	if _, err := New(cfg); err != nil {
		t.Errorf("writable server should create it: %v", err)
	}
}

func TestSharedFoldersAndMove(t *testing.T) {
	ts, dir := sharedServer(t, true)
	base := ts.URL + "/api/shared"

	// A sample put into a new folder creates it (privately) and lists with its folder.
	code, m := do(t, "PUT", base+"/sample?folder=BankX&name=GetStatement", `{"tag":"Req","xml":"<a/>"}`)
	if code != 200 || m["folder"] != "BankX" {
		t.Fatalf("put into folder: %d %v", code, m)
	}
	v := int64(m["updatedAt"].(float64))
	if _, err := os.Stat(filepath.Join(dir, "BankX", "GetStatement [Req].xml")); err != nil {
		t.Fatalf("file not in folder: %v", err)
	}
	// The same name at the top level is a different sample.
	if code, _ := do(t, "PUT", base+"/sample?name=GetStatement", `{"xml":"<top/>"}`); code != 200 {
		t.Errorf("same name at top level: %d", code)
	}
	code, m = do(t, "GET", base, "")
	folders, _ := m["folders"].([]any)
	samples, _ := m["samples"].([]any)
	if code != 200 || len(folders) != 1 || folders[0] != "BankX" || len(samples) != 2 {
		t.Errorf("list: %d %v", code, m)
	}
	if code, m := do(t, "GET", base+"/sample?folder=bankx&name=getstatement", ""); code != 200 || m["xml"] != "<a/>" || m["folder"] != "BankX" {
		t.Errorf("get from folder: %d %v", code, m)
	}

	// PATCH: move to another folder and retag; requires ifUpdatedAt.
	u := base + "/sample?folder=BankX&name=GetStatement"
	if code, _ := do(t, "PATCH", u, `{"folder":"BankY","name":"Renamed","tag":"Res"}`); code != 400 {
		t.Errorf("PATCH without ifUpdatedAt = %d, want 400", code)
	}
	if code, m := do(t, "PATCH", u, `{"folder":"BankY","name":"Renamed","tag":"Res","ifUpdatedAt":1}`); code != 409 || m["code"] != "conflict" {
		t.Errorf("stale PATCH: %d %v", code, m)
	}
	code, m = do(t, "PATCH", u, `{"folder":"BankY","name":"Renamed","tag":"Res","ifUpdatedAt":`+strconv.FormatInt(v, 10)+`}`)
	if code != 200 || m["folder"] != "BankY" || m["name"] != "Renamed" {
		t.Fatalf("PATCH: %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(dir, "BankY", "Renamed [Res].xml")); err != nil {
		t.Errorf("moved file missing: %v", err)
	}
	if code, _ := do(t, "PATCH", base+"/sample?folder=BankY&name=Renamed", `{"folder":"../x","name":"n","ifUpdatedAt":`+strconv.FormatInt(int64(m["updatedAt"].(float64)), 10)+`}`); code != 400 {
		t.Errorf("traversal in PATCH target = %d, want 400", code)
	}

	// Folder endpoints.
	if code, _ := do(t, "PUT", base+"/folder?name=Empty", ""); code != 200 {
		t.Errorf("create folder: %d", code)
	}
	if code, m := do(t, "PUT", base+"/folder?name=empty", ""); code != 409 || m["code"] != "exists" {
		t.Errorf("duplicate folder: %d %v", code, m)
	}
	if code, _ := do(t, "PUT", base+"/folder?name=..", ""); code != 400 {
		t.Errorf("bad folder name: %d", code)
	}
	if code, m := do(t, "DELETE", base+"/folder?name=BankY", ""); code != 409 || m["code"] != "notempty" {
		t.Errorf("non-empty folder delete: %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(dir, "BankY", "Renamed [Res].xml")); err != nil {
		t.Errorf("a refused folder delete must not touch samples: %v", err)
	}
	if code, _ := do(t, "PATCH", base+"/folder?name=Empty", `{"name":"Archive"}`); code != 200 {
		t.Errorf("rename folder: %d", code)
	}
	if code, _ := do(t, "DELETE", base+"/folder?name=Archive", ""); code != http.StatusNoContent {
		t.Errorf("delete empty folder: %d", code)
	}
	if code, _ := do(t, "DELETE", base+"/folder?name=Archive", ""); code != 404 {
		t.Errorf("delete missing folder: %d", code)
	}
}

func TestSharedFolderWritesNeedWritable(t *testing.T) {
	ts, dir := sharedServer(t, false)
	os.MkdirAll(filepath.Join(dir, "Keep"), 0o700)
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/shared/folder?name=New", ""},
		{"PATCH", "/api/shared/folder?name=Keep", `{"name":"X"}`},
		{"DELETE", "/api/shared/folder?name=Keep", ""},
		{"PATCH", "/api/shared/sample?name=a", `{"name":"b","ifUpdatedAt":1}`},
	} {
		if code, _ := do(t, c.method, ts.URL+c.path, c.body); code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", c.method, c.path, code)
		}
	}
	if des, _ := os.ReadDir(dir); len(des) != 1 || des[0].Name() != "Keep" {
		t.Errorf("read-only server changed the folder: %v", des)
	}
}
