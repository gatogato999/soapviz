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
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ts(offsetSeconds int) time.Time {
	return time.Now().Add(time.Duration(offsetSeconds) * time.Second)
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
	write(t, dir, "five [Fives].xml", "<a/>") // brackets are not allowed in a name
	outside := filepath.Join(t.TempDir(), "secret.xml")
	os.WriteFile(outside, []byte("<secret/>"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.xml")); err != nil {
		t.Skip("symlinks unavailable")
	}

	l, skipped, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, i := range l.Samples {
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
	if _, err := s.Get("", "link"); !errors.Is(err, ErrNotFound) {
		t.Errorf("symlink must not be readable, got %v", err)
	}
}

func TestPutGetDeleteAndConflicts(t *testing.T) {
	s, dir := newStore(t)
	info, err := s.Put("", "Req One", "Req", "<a>1</a>", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "Req One [Req].xml")
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
	got, err := s.Get("", "req one") // case-insensitive
	if err != nil || got.XML != "<a>1</a>" || got.Tag != "Req" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Put("", "REQ ONE", "Req", "<a>2</a>", nil); !errors.Is(err, ErrExists) {
		t.Errorf("want ErrExists, got %v", err)
	}
	stale := info.UpdatedAt - 5000
	var ce *ConflictError
	if _, err := s.Put("", "Req One", "Req", "<a>2</a>", &stale); !errors.As(err, &ce) || ce.UpdatedAt != info.UpdatedAt {
		t.Errorf("want ConflictError with current time, got %v", err)
	}
	if _, err := s.Put("", "Req One", "Res", "<a>3</a>", &info.UpdatedAt); err != nil { // retag renames the file
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("old file should be gone: %v", err)
	}
	if l, _, _ := s.List(); len(l.Samples) != 1 || l.Samples[0].Tag != "Res" {
		t.Errorf("list after retag: %+v", l)
	}
	des, _ := os.ReadDir(dir)
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", d.Name())
		}
	}
	if err := s.Delete("", "req one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("", "Req One"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.Delete("", "Req One"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
}

func TestValidation(t *testing.T) {
	s, dir := newStore(t)
	for _, n := range []string{"", " x", "x ", "../x", "a/b", `a\b`, "a:b", "a[b]", ".x", "..", "a|b", "a\x00b", strings.Repeat("x", 81), strings.Repeat("é", 80) + "é"} {
		if _, err := s.Put("", n, "", "<a/>", nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: want ErrInvalid, got %v", n, err)
		}
		if n != "" { // the same rules apply to folder names
			if _, err := s.Put(n, "ok", "", "<a/>", nil); !errors.Is(err, ErrInvalid) {
				t.Errorf("folder %q: want ErrInvalid, got %v", n, err)
			}
		}
	}
	for _, tg := range []string{"12", "ab1", "abcde", "a b", "é"} {
		if _, err := s.Put("", "ok", tg, "<a/>", nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("tag %q: want ErrInvalid, got %v", tg, err)
		}
	}
	if _, err := s.Put("", "ok", "", "   ", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty content: %v", err)
	}
	if _, err := s.Put("", "ok", "", "\xff\xfe", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("non-UTF-8: %v", err)
	}
	small, _ := Open(dir, true, 10)
	if _, err := small.Put("", "big", "", strings.Repeat("a", 11), nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("rejected writes left files or folders: %v", des)
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
	os.Chtimes(filepath.Join(dir, "Same [Req].xml"), ts(-3600), ts(-3600))
	write(t, dir, "same [Res].xml", "<new/>")
	l, skipped, _ := s.List()
	if len(l.Samples) != 1 || l.Samples[0].Tag != "Res" || len(skipped) != 1 {
		t.Errorf("l=%+v skipped=%v", l, skipped)
	}
}

// ---- folders ----

func TestListFolders(t *testing.T) {
	s, dir := newStore(t)
	write(t, dir, "Top.xml", "<t/>")
	write(t, dir, "BankX/GetStatement [Req].xml", "<a/>")
	write(t, dir, "BankX/GetStatement [Res].xml", "<dup/>") // same name, same folder: a duplicate
	write(t, dir, "BankX/Other.xml", "<o/>")
	write(t, dir, "BankY/Top.xml", "<y/>") // the same name in another folder is fine
	os.MkdirAll(filepath.Join(dir, "Empty"), 0o700)
	os.MkdirAll(filepath.Join(dir, "BankX", "nested"), 0o700) // nested folders are ignored
	write(t, dir, "BankX/nested/Deep.xml", "<d/>")
	os.MkdirAll(filepath.Join(dir, "bad:folder"), 0o700)
	write(t, dir, "bad:folder/A.xml", "<a/>")
	os.MkdirAll(filepath.Join(dir, ".hiddenfolder"), 0o700)
	write(t, dir, ".hiddenfolder/A.xml", "<a/>")
	outside := t.TempDir()
	write(t, outside, "Secret.xml", "<s/>")
	if err := os.Symlink(outside, filepath.Join(dir, "Linked")); err != nil {
		t.Skip("symlinks unavailable")
	}

	l, skipped, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Folders, ",") != "BankX,BankY,Empty" {
		t.Errorf("folders = %v (symlinked, hidden and badly named folders must not appear)", l.Folders)
	}
	var names []string
	for _, i := range l.Samples {
		names = append(names, i.Folder+"/"+i.Name)
	}
	if got := strings.Join(names, ","); got != "/Top,BankX/GetStatement,BankX/Other,BankY/Top" {
		t.Errorf("samples = %s", got)
	}
	joined := strings.Join(skipped, "|")
	for _, w := range []string{"nested folders are not supported", "bad:folder (folder name not allowed)"} {
		if !strings.Contains(joined, w) {
			t.Errorf("skipped should mention %q, got %v", w, skipped)
		}
	}
	if _, err := s.Get("Linked", "Secret"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a symlinked folder must not be readable: %v", err)
	}
	got, err := s.Get("bankx", "other") // case-insensitive in both parts
	if err != nil || got.XML != "<o/>" || got.Folder != "BankX" {
		t.Errorf("get in folder: %+v %v", got, err)
	}
}

func TestPutCreatesAndCanonicalizesFolders(t *testing.T) {
	s, dir := newStore(t)
	info, err := s.Put("BankX", "A", "Req", "<a/>", nil)
	if err != nil || info.Folder != "BankX" {
		t.Fatalf("%+v %v", info, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "BankX")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("folder not created privately: %v %v", fi, err)
	}
	info2, err := s.Put("bankx", "B", "", "<b/>", nil) // different case: same folder
	if err != nil || info2.Folder != "BankX" {
		t.Fatalf("folder case should canonicalize: %+v %v", info2, err)
	}
	if des, _ := os.ReadDir(dir); len(des) != 1 {
		t.Errorf("a second folder was created: %v", des)
	}
	// Same name in a different folder is allowed; in the same folder it is not.
	if _, err := s.Put("BankY", "A", "", "<a/>", nil); err != nil {
		t.Errorf("same name, other folder: %v", err)
	}
	if _, err := s.Put("BANKX", "a", "", "<a/>", nil); !errors.Is(err, ErrExists) {
		t.Errorf("same name, same folder: %v", err)
	}
	// Overwriting keeps it in its folder and replaces the right file.
	if _, err := s.Put("bankx", "A", "Req", "<a2/>", &info.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "BankX", "A [Req].xml")); string(b) != "<a2/>" {
		t.Errorf("content = %q", b)
	}
}

func TestMove(t *testing.T) {
	s, dir := newStore(t)
	a, _ := s.Put("", "Doc", "Req", "<doc/>", nil)

	m, err := s.Move("", "Doc", Target{Folder: "BankX", Name: "Doc2", Tag: "Res"}, a.UpdatedAt)
	if err != nil || m.Folder != "BankX" || m.Name != "Doc2" || m.Tag != "Res" {
		t.Fatalf("move: %+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Doc [Req].xml")); !os.IsNotExist(err) {
		t.Error("source should be gone")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "BankX", "Doc2 [Res].xml")); string(b) != "<doc/>" {
		t.Errorf("content changed by move: %q", b)
	}
	if _, err := s.Move("", "Doc", Target{Name: "x"}, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("moving a missing sample: %v", err)
	}
	var ce *ConflictError
	if _, err := s.Move("BankX", "Doc2", Target{Folder: "BankX", Name: "Doc3"}, m.UpdatedAt-5000); !errors.As(err, &ce) {
		t.Errorf("stale move: %v", err)
	}

	// Destination taken by a different sample.
	b, _ := s.Put("BankX", "Taken", "", "<t/>", nil)
	_ = b
	if _, err := s.Move("BankX", "Doc2", Target{Folder: "bankx", Name: "taken"}, m.UpdatedAt); !errors.Is(err, ErrExists) {
		t.Errorf("move onto an existing sample: %v", err)
	}
	// Retag / case change in place is not a conflict with itself.
	m2, err := s.Move("BankX", "Doc2", Target{Folder: "BankX", Name: "doc2", Tag: "Err"}, m.UpdatedAt)
	if err != nil || m2.Name != "doc2" || m2.Tag != "Err" {
		t.Errorf("in-place rename: %+v %v", m2, err)
	}
	if _, err := s.Move("BankX", "doc2", Target{Folder: "../x", Name: "n"}, m2.UpdatedAt); !errors.Is(err, ErrInvalid) {
		t.Errorf("traversal in target folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x")); !os.IsNotExist(err) {
		t.Error("move escaped the store")
	}
	des, _ := os.ReadDir(filepath.Join(dir, "BankX"))
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("leftover %s", d.Name())
		}
	}
}

func TestFolderOps(t *testing.T) {
	s, dir := newStore(t)
	if err := s.CreateFolder("BankX"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFolder("bankx"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate folder (case-insensitive): %v", err)
	}
	for _, bad := range []string{"", "..", "a/b", ".h", "a[b]"} {
		if err := s.CreateFolder(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("folder %q: %v", bad, err)
		}
	}
	s.Put("BankX", "A", "Req", "<a/>", nil)
	if err := s.DeleteFolder("BankX"); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("deleting a folder with samples must fail: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "BankX", "A [Req].xml")); err != nil {
		t.Errorf("sample must survive a refused delete: %v", err)
	}
	write(t, dir, "BankX/notes.txt", "x") // admin's own file: still "not empty" after the sample goes
	if err := s.RenameFolder("BankX", "BankZ"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("BankZ", "A"); err != nil {
		t.Errorf("sample should follow its folder: %v", err)
	}
	if err := s.RenameFolder("nope", "Q"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing: %v", err)
	}
	s.CreateFolder("Other")
	if err := s.RenameFolder("BankZ", "other"); !errors.Is(err, ErrExists) {
		t.Errorf("rename onto an existing folder: %v", err)
	}
	if err := s.RenameFolder("BankZ", "bankz"); err != nil { // case-only change is allowed
		t.Errorf("case change: %v", err)
	}
	s.Delete("bankz", "A")
	if err := s.DeleteFolder("bankz"); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("a folder with an admin's file is not empty: %v", err)
	}
	if err := s.DeleteFolder("Other"); err != nil {
		t.Errorf("empty folder: %v", err)
	}
	if err := s.DeleteFolder("Other"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
}
