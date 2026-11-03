// Package shared is the team sample library: a folder of plain .xml files,
// one per sample. A tag is part of the file name: "GetStatement [Req].xml".
// Admins can drop files in by hand; the server can also write them when it
// is configured to. Listing and reading never follow symlinks.
package shared

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	MaxNameRunes = 80
	maxNameBytes = 150 // keeps "name [tag].xml" under common 255-byte file name limits
	maxEntries   = 1000
)

var (
	ErrNotFound = errors.New("sample not found")
	ErrExists   = errors.New("a sample with that name already exists")
	ErrTooLarge = errors.New("sample is too large")
	// ErrInvalid wraps validation failures (bad name, bad tag, empty or non-UTF-8 content).
	ErrInvalid = errors.New("invalid sample")
)

// ConflictError means the sample changed since the caller last read it.
type ConflictError struct{ UpdatedAt int64 }

func (e *ConflictError) Error() string { return "the sample was changed by someone else" }

type Info struct {
	Name      string `json:"name"`
	Tag       string `json:"tag,omitempty"`
	Bytes     int64  `json:"bytes"`
	UpdatedAt int64  `json:"updatedAt"` // file modification time, Unix milliseconds
}

type Sample struct {
	Info
	XML string `json:"xml"`
}

type Store struct {
	dir      string
	maxBytes int64
	mu       sync.Mutex // serializes writes; reads scan the directory each time
}

// Open prepares dir. A missing directory is created if the store is
// writable and is an error otherwise, which catches typos in config.ini.
func Open(dir string, writable bool, maxBytes int64) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if !writable {
			return nil, fmt.Errorf("shared_dir %q does not exist", dir)
		}
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case !st.IsDir():
		return nil, fmt.Errorf("shared_dir %q is not a directory", dir)
	}
	return &Store{dir: abs, maxBytes: maxBytes}, nil
}

// ValidateName enforces the rules shared with the browser: 1 to 80 characters,
// no leading or trailing space, no leading dot, none of / \ : * ? " < > | [ ].
func ValidateName(n string) error {
	switch {
	case n == "" || n != strings.TrimSpace(n):
		return fmt.Errorf("%w: name must not be empty or start or end with a space", ErrInvalid)
	case !utf8.ValidString(n):
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalid)
	case utf8.RuneCountInString(n) > MaxNameRunes || len(n) > maxNameBytes:
		return fmt.Errorf("%w: name is too long (max %d characters)", ErrInvalid, MaxNameRunes)
	case n[0] == '.':
		return fmt.Errorf("%w: name must not start with a dot", ErrInvalid)
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|[]`, r) {
			return fmt.Errorf(`%w: name must not contain control characters or any of / \ : * ? " < > | [ ]`, ErrInvalid)
		}
	}
	return nil
}

var tagRE = regexp.MustCompile(`^[A-Za-z]{1,4}$`)

// ValidateTag accepts "" (no tag) or 1 to 4 ASCII letters.
func ValidateTag(t string) error {
	if t != "" && !tagRE.MatchString(t) {
		return fmt.Errorf("%w: a tag is 1 to 4 letters", ErrInvalid)
	}
	return nil
}

func fileName(name, tag string) string {
	if tag != "" {
		return name + " [" + tag + "].xml"
	}
	return name + ".xml"
}

var suffixRE = regexp.MustCompile(`^(.*\S)\s*\[([A-Za-z]{1,4})\]$`)

// parseFileName turns "Name [Tag].xml" into its parts. ok is false for files
// that are not samples or whose names break the naming rules.
func parseFileName(base string) (name, tag string, ok bool) {
	if len(base) < 5 || !strings.EqualFold(base[len(base)-4:], ".xml") {
		return "", "", false
	}
	stem := base[:len(base)-4]
	if m := suffixRE.FindStringSubmatch(stem); m != nil {
		stem, tag = m[1], m[2]
	}
	if ValidateName(stem) != nil {
		return "", "", false
	}
	return stem, tag, true
}

type entry struct {
	Info
	file string
}

// scan lists valid regular files. Duplicate names (ignoring case) keep the
// newest file. skipped holds files that were ignored and why.
func (s *Store) scan() (out []entry, skipped []string, err error) {
	des, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, nil, err
	}
	byKey := map[string]int{}
	for _, de := range des {
		base := de.Name()
		if strings.HasPrefix(base, ".") || de.IsDir() || !strings.HasSuffix(strings.ToLower(base), ".xml") {
			continue
		}
		if !de.Type().IsRegular() { // symlinks, sockets, devices
			skipped = append(skipped, base+" (not a regular file)")
			continue
		}
		name, tag, ok := parseFileName(base)
		if !ok {
			skipped = append(skipped, base+" (name not allowed)")
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		if fi.Size() > s.maxBytes {
			skipped = append(skipped, base+" (too large)")
			continue
		}
		e := entry{Info{name, tag, fi.Size(), fi.ModTime().UnixMilli()}, base}
		key := strings.ToLower(name)
		if i, dup := byKey[key]; dup {
			if e.UpdatedAt > out[i].UpdatedAt {
				skipped = append(skipped, out[i].file+" (duplicate name)")
				out[i] = e
			} else {
				skipped = append(skipped, base+" (duplicate name)")
			}
			continue
		}
		if len(out) >= maxEntries {
			skipped = append(skipped, base+" (library limit reached)")
			continue
		}
		byKey[key] = len(out)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, skipped, nil
}

func (s *Store) find(name string) (*entry, error) {
	es, _, err := s.scan()
	if err != nil {
		return nil, err
	}
	for i := range es {
		if strings.EqualFold(es[i].Name, name) {
			return &es[i], nil
		}
	}
	return nil, ErrNotFound
}

// List returns all samples without their contents, plus the files it ignored.
func (s *Store) List() ([]Info, []string, error) {
	es, skipped, err := s.scan()
	infos := make([]Info, len(es))
	for i, e := range es {
		infos[i] = e.Info
	}
	return infos, skipped, err
}

func (s *Store) Get(name string) (Sample, error) {
	e, err := s.find(name)
	if err != nil {
		return Sample{}, err
	}
	f, err := os.Open(filepath.Join(s.dir, e.file))
	if err != nil {
		return Sample{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return Sample{}, ErrNotFound
	}
	if fi.Size() > s.maxBytes {
		return Sample{}, ErrTooLarge
	}
	buf := make([]byte, fi.Size())
	if _, err := f.ReadAt(buf, 0); err != nil && fi.Size() > 0 {
		return Sample{}, err
	}
	if !utf8.Valid(buf) {
		return Sample{}, fmt.Errorf("%w: file is not valid UTF-8 text", ErrInvalid)
	}
	return Sample{Info: Info{e.Name, e.Tag, fi.Size(), fi.ModTime().UnixMilli()}, XML: string(buf)}, nil
}

// Put creates or replaces a sample. To replace one that exists, pass the
// UpdatedAt you last saw; without it an existing name is ErrExists, and with a
// stale value the result is a *ConflictError. The file is written to a temp
// file and renamed into place, so readers never see a partial sample.
func (s *Store) Put(name, tag, xml string, ifUpdatedAt *int64) (Info, error) {
	if err := ValidateName(name); err != nil {
		return Info{}, err
	}
	if err := ValidateTag(tag); err != nil {
		return Info{}, err
	}
	switch {
	case !utf8.ValidString(xml):
		return Info{}, fmt.Errorf("%w: content is not valid UTF-8", ErrInvalid)
	case strings.TrimSpace(xml) == "":
		return Info{}, fmt.Errorf("%w: content is empty", ErrInvalid)
	case int64(len(xml)) > s.maxBytes:
		return Info{}, ErrTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.find(name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Info{}, err
	}
	if old != nil {
		if ifUpdatedAt == nil {
			return Info{}, ErrExists
		}
		if *ifUpdatedAt != old.UpdatedAt {
			return Info{}, &ConflictError{UpdatedAt: old.UpdatedAt}
		}
	}

	target := filepath.Join(s.dir, fileName(name, tag))
	if filepath.Dir(target) != s.dir { // defense in depth; ValidateName already forbids separators
		return Info{}, fmt.Errorf("%w: bad name", ErrInvalid)
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return Info{}, err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.WriteString(xml); err != nil {
		tmp.Close()
		return Info{}, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return Info{}, err
	}
	if err := tmp.Close(); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return Info{}, err
	}
	if old != nil && old.file != filepath.Base(target) { // the tag changed, so the file name did
		_ = os.Remove(filepath.Join(s.dir, old.file))
	}
	fi, err := os.Stat(target)
	if err != nil {
		return Info{}, err
	}
	return Info{name, tag, fi.Size(), fi.ModTime().UnixMilli()}, nil
}

func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.find(name)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.dir, e.file))
}
