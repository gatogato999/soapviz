package shared

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, true, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListParsesFileNames(t *testing.T) {
	s, dir := newStore(t)
	write(t, dir, "GetStatement [Req].xml", "<a/>")
	write(t, dir, "Plain.xml", "<a/>")
	write(t, dir, "Upper.XML", "<a/>")
	write(t, dir, "Spaced[Res].xml", "<a/>")
	write(t, dir, ".hidden.xml", "<a/>")
	write(t, dir, "notes.txt", "x")
	write(t, dir, "bad:name.xml", "<a/>")
	write(t, dir, "five [Fives].xml", "<a/>") // not a tag: the name stays "five [Fives]" and brackets are not allowed
	os.Mkdir(filepath.Join(dir, "sub.xml"), 0o700)
	outside := filepath.Join(t.TempDir(), "secret.xml")
	os.WriteFile(outside, []byte("<secret/>"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.xml")); err != nil {
		t.Skip("symlinks unavailable")
	}

	infos, skipped, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, i := range infos {
		got[i.Name] = i.Tag
	}
	want := map[string]string{"GetStatement": "Req", "Plain": "", "Upper": "", "Spaced": "Res"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%q: got tag %q (present %v), want %q", k, g, ok, v)
		}
	}
	joined := strings.Join(skipped, "|")
	for _, w := range []string{"link.xml (not a regular file)", "bad:name.xml (name not allowed)"} {
		if !strings.Contains(joined, w) {
			t.Errorf("skipped should mention %q, got %v", w, skipped)
		}
	}
	if _, err := s.Get("link"); !errors.Is(err, ErrNotFound) {
		t.Errorf("symlink must not be readable, got %v", err)
	}
}

func TestPutGetDeleteAndConflicts(t *testing.T) {
	s, dir := newStore(t)
	info, err := s.Put("Req One", "Req", "<a>1</a>", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Req One [Req].xml")); err != nil {
		t.Fatalf("file name wrong: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "Req One [Req].xml")); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", fi.Mode().Perm())
	}
	got, err := s.Get("req one") // case-insensitive
	if err != nil || got.XML != "<a>1</a>" || got.Tag != "Req" {
		t.Fatalf("get: %+v %v", got, err)
	}

	if _, err := s.Put("REQ ONE", "Req", "<a>2</a>", nil); !errors.Is(err, ErrExists) {
		t.Errorf("want ErrExists, got %v", err)
	}
	stale := info.UpdatedAt - 5000
	var ce *ConflictError
	if _, err := s.Put("Req One", "Req", "<a>2</a>", &stale); !errors.As(err, &ce) || ce.UpdatedAt != info.UpdatedAt {
		t.Errorf("want ConflictError with current time, got %v", err)
	}

	// Changing the tag renames the file and removes the old one.
	info2, err := s.Put("Req One", "Res", "<a>3</a>", &info.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Req One [Req].xml")); !os.IsNotExist(err) {
		t.Errorf("old file should be gone: %v", err)
	}
	if infos, _, _ := s.List(); len(infos) != 1 || infos[0].Tag != "Res" {
		t.Errorf("list after retag: %+v", infos)
	}
	// No temp files left behind.
	des, _ := os.ReadDir(dir)
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", d.Name())
		}
	}

	if err := s.Delete("req one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("Req One"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.Delete("Req One"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
	_ = info2
}

func TestValidation(t *testing.T) {
	s, dir := newStore(t)
	for _, n := range []string{"", " x", "x ", "../x", "a/b", `a\b`, "a:b", "a[b]", ".x", "..", "a|b", "a\x00b", strings.Repeat("x", 81), strings.Repeat("é", 80) + "é"} {
		if _, err := s.Put(n, "", "<a/>", nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: want ErrInvalid, got %v", n, err)
		}
	}
	for _, tg := range []string{"12", "ab1", "abcde", "a b", "é"} {
		if _, err := s.Put("ok", tg, "<a/>", nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("tag %q: want ErrInvalid, got %v", tg, err)
		}
	}
	if _, err := s.Put("ok", "", "   ", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty content: %v", err)
	}
	if _, err := s.Put("ok", "", "\xff\xfe", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("non-UTF-8: %v", err)
	}
	small, _ := Open(dir, true, 10)
	if _, err := small.Put("big", "", strings.Repeat("a", 11), nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	// Nothing was written outside the directory or inside it.
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("rejected writes left files: %v", des)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x.xml")); !os.IsNotExist(err) {
		t.Errorf("path traversal wrote outside the store")
	}
}

func TestOpen(t *testing.T) {
	base := t.TempDir()
	if _, err := Open(filepath.Join(base, "missing"), false, 1); err == nil {
		t.Error("read-only open of a missing dir must fail")
	}
	d := filepath.Join(base, "new")
	if _, err := Open(d, true, 1); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("created dir: %v %v", fi, err)
	}
	f := filepath.Join(base, "file")
	os.WriteFile(f, nil, 0o600)
	if _, err := Open(f, true, 1); err == nil {
		t.Error("a file is not a directory")
	}
}

func TestDuplicateNamesKeepNewest(t *testing.T) {
	s, dir := newStore(t)
	write(t, dir, "Same [Req].xml", "<old/>")
	old := filepath.Join(dir, "Same [Req].xml")
	os.Chtimes(old, ts(-3600), ts(-3600))
	write(t, dir, "same [Res].xml", "<new/>")
	infos, skipped, _ := s.List()
	if len(infos) != 1 || infos[0].Tag != "Res" || len(skipped) != 1 {
		t.Errorf("infos=%+v skipped=%v", infos, skipped)
	}
}

func ts(offsetSeconds int) (t time.Time) {
	return time.Now().Add(time.Duration(offsetSeconds) * time.Second)
}
