// Package shared is the team sample library: a folder of plain .xml files,
// one per sample, optionally grouped in one level of subfolders:
//
//	shared_dir/GetStatement [Req].xml
//	shared_dir/BankX/GetStatement [Res].xml
//
// A tag is part of the file name. Admins can manage the files by hand; the
// server can also write them when configured to. Nothing here follows symlinks.
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
	"syscall"
	"unicode/utf8"
)

const (
	MaxNameRunes = 80
	maxNameBytes = 150 // keeps "name [tag].xml" under common 255-byte file name limits
	maxEntries   = 1000
	maxFolders   = 200
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrNotEmpty = errors.New("the folder is not empty")
	ErrTooLarge = errors.New("sample is too large")
	// ErrInvalid wraps validation failures (bad name, bad tag, empty or non-UTF-8 content).
	ErrInvalid = errors.New("invalid")
)

// ConflictError means the sample changed since the caller last read it.
type ConflictError struct{ UpdatedAt int64 }

func (e *ConflictError) Error() string { return "the sample was changed by someone else" }

type Info struct {
	Folder    string `json:"folder,omitempty"`
	Name      string `json:"name"`
	Tag       string `json:"tag,omitempty"`
	Bytes     int64  `json:"bytes"`
	UpdatedAt int64  `json:"updatedAt"` // file modification time, Unix milliseconds
}

type Sample struct {
	Info
	XML string `json:"xml"`
}

// Listing is everything in the library, without sample contents. Folders
// includes empty ones.
type Listing struct {
	Samples []Info   `json:"samples"`
	Folders []string `json:"folders"`
}

// Target is where Move puts a sample.
type Target struct{ Folder, Name, Tag string }

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

// ValidateName enforces the rules for sample and folder names, shared with the
// browser: 1 to 80 characters, no leading or trailing space, no leading dot,
// none of / \ : * ? " < > | [ ].
func ValidateName(n string) error {
	switch {
	case n == "" || n != strings.TrimSpace(n):
		return fmt.Errorf("%w: a name must not be empty or start or end with a space", ErrInvalid)
	case !utf8.ValidString(n):
		return fmt.Errorf("%w: a name is not valid UTF-8", ErrInvalid)
	case utf8.RuneCountInString(n) > MaxNameRunes || len(n) > maxNameBytes:
		return fmt.Errorf("%w: a name is too long (max %d characters)", ErrInvalid, MaxNameRunes)
	case n[0] == '.':
		return fmt.Errorf("%w: a name must not start with a dot", ErrInvalid)
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|[]`, r) {
			return fmt.Errorf(`%w: a name must not contain control characters or any of / \ : * ? " < > | [ ]`, ErrInvalid)
		}
	}
	return nil
}

// ValidateFolder accepts "" (the top level) or a valid name. Folders are one level deep.
func ValidateFolder(f string) error {
	if f == "" {
		return nil
	}
	return ValidateName(f)
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
	file string // base file name inside its folder
}

type snapshot struct {
	samples []entry  // sorted by folder, then name, ignoring case
	folders []string // actual directory names, sorted ignoring case
	skipped []string
}

func (sn *snapshot) folderDir(folder string) (string, bool) {
	for _, f := range sn.folders {
		if strings.EqualFold(f, folder) {
			return f, true
		}
	}
	return "", false
}

func (sn *snapshot) find(folder, name string) *entry {
	for i := range sn.samples {
		e := &sn.samples[i]
		if strings.EqualFold(e.Folder, folder) && strings.EqualFold(e.Name, name) {
			return e
		}
	}
	return nil
}

// scan reads the library. Anything that is not a plain, validly named .xml
// file (or a plain, validly named top-level directory) is skipped and noted.
func (s *Store) scan() (snapshot, error) {
	var sn snapshot
	root, err := os.ReadDir(s.dir)
	if err != nil {
		return sn, err
	}
	seen := map[string]int{} // folder\x00name (lowercase) -> index in sn.samples
	collect := func(folder, path string, des []os.DirEntry) {
		for _, de := range des {
			base := de.Name()
			label := base
			if folder != "" {
				label = folder + "/" + base
			}
			if strings.HasPrefix(base, ".") {
				continue
			}
			if de.IsDir() {
				if folder != "" {
					sn.skipped = append(sn.skipped, label+" (nested folders are not supported)")
				}
				continue
			}
			if !strings.HasSuffix(strings.ToLower(base), ".xml") {
				continue
			}
			if !de.Type().IsRegular() { // symlinks, sockets, devices
				sn.skipped = append(sn.skipped, label+" (not a regular file)")
				continue
			}
			name, tag, ok := parseFileName(base)
			if !ok {
				sn.skipped = append(sn.skipped, label+" (name not allowed)")
				continue
			}
			fi, err := de.Info()
			if err != nil {
				continue
			}
			if fi.Size() > s.maxBytes {
				sn.skipped = append(sn.skipped, label+" (too large)")
				continue
			}
			e := entry{Info{folder, name, tag, fi.Size(), fi.ModTime().UnixMilli()}, base}
			key := strings.ToLower(folder) + "\x00" + strings.ToLower(name)
			if i, dup := seen[key]; dup {
				if e.UpdatedAt > sn.samples[i].UpdatedAt {
					sn.skipped = append(sn.skipped, sn.samples[i].file+" (duplicate name)")
					sn.samples[i] = e
				} else {
					sn.skipped = append(sn.skipped, label+" (duplicate name)")
				}
				continue
			}
			if len(sn.samples) >= maxEntries {
				sn.skipped = append(sn.skipped, label+" (library limit reached)")
				continue
			}
			seen[key] = len(sn.samples)
			sn.samples = append(sn.samples, e)
		}
	}

	collect("", s.dir, root)
	for _, de := range root {
		base := de.Name()
		if !de.IsDir() || strings.HasPrefix(base, ".") {
			continue
		}
		if ValidateName(base) != nil {
			sn.skipped = append(sn.skipped, base+" (folder name not allowed)")
			continue
		}
		if _, dup := sn.folderDir(base); dup {
			sn.skipped = append(sn.skipped, base+" (duplicate folder name)")
			continue
		}
		if len(sn.folders) >= maxFolders {
			sn.skipped = append(sn.skipped, base+" (folder limit reached)")
			continue
		}
		sub, err := os.ReadDir(filepath.Join(s.dir, base))
		if err != nil {
			sn.skipped = append(sn.skipped, base+" (cannot read folder)")
			continue
		}
		sn.folders = append(sn.folders, base)
		collect(base, filepath.Join(s.dir, base), sub)
	}
	sort.Slice(sn.folders, func(i, j int) bool { return strings.ToLower(sn.folders[i]) < strings.ToLower(sn.folders[j]) })
	sort.Slice(sn.samples, func(i, j int) bool {
		a, b := sn.samples[i], sn.samples[j]
		if fa, fb := strings.ToLower(a.Folder), strings.ToLower(b.Folder); fa != fb {
			return fa < fb
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return sn, nil
}

func (s *Store) path(folder, file string) string {
	if folder == "" {
		return filepath.Join(s.dir, file)
	}
	return filepath.Join(s.dir, folder, file)
}

// List returns every sample (without contents) and every folder, plus the files it ignored.
func (s *Store) List() (Listing, []string, error) {
	sn, err := s.scan()
	l := Listing{Samples: make([]Info, len(sn.samples)), Folders: append([]string{}, sn.folders...)}
	for i, e := range sn.samples {
		l.Samples[i] = e.Info
	}
	return l, sn.skipped, err
}

func (s *Store) Get(folder, name string) (Sample, error) {
	sn, err := s.scan()
	if err != nil {
		return Sample{}, err
	}
	e := sn.find(folder, name)
	if e == nil {
		return Sample{}, fmt.Errorf("sample %w", ErrNotFound)
	}
	f, err := os.Open(s.path(e.Folder, e.file))
	if err != nil {
		return Sample{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return Sample{}, fmt.Errorf("sample %w", ErrNotFound)
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
	return Sample{Info: Info{e.Folder, e.Name, e.Tag, fi.Size(), fi.ModTime().UnixMilli()}, XML: string(buf)}, nil
}

// resolveFolder returns the folder's actual directory name, creating the
// directory when it does not exist yet. A name that differs only in case from
// an existing folder maps to that folder. Callers hold s.mu.
func (s *Store) resolveFolder(sn *snapshot, folder string) (string, error) {
	if folder == "" {
		return "", nil
	}
	if actual, ok := sn.folderDir(folder); ok {
		return actual, nil
	}
	if len(sn.folders) >= maxFolders {
		return "", fmt.Errorf("%w: too many folders", ErrInvalid)
	}
	if err := os.Mkdir(filepath.Join(s.dir, folder), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	sn.folders = append(sn.folders, folder)
	return folder, nil
}

func validateContent(xml string, max int64) error {
	switch {
	case !utf8.ValidString(xml):
		return fmt.Errorf("%w: content is not valid UTF-8", ErrInvalid)
	case strings.TrimSpace(xml) == "":
		return fmt.Errorf("%w: content is empty", ErrInvalid)
	case int64(len(xml)) > max:
		return ErrTooLarge
	}
	return nil
}

func validateTarget(t Target) error {
	if err := ValidateFolder(t.Folder); err != nil {
		return err
	}
	if err := ValidateName(t.Name); err != nil {
		return err
	}
	return ValidateTag(t.Tag)
}

// Put creates or replaces a sample, creating its folder if needed. To replace
// one that exists, pass the UpdatedAt you last saw; without it an existing
// name is ErrExists, and with a stale value the result is a *ConflictError.
// The file is written to a temp file in the same folder and renamed into
// place, so readers never see a partial sample.
func (s *Store) Put(folder, name, tag, xml string, ifUpdatedAt *int64) (Info, error) {
	if err := validateTarget(Target{folder, name, tag}); err != nil {
		return Info{}, err
	}
	if err := validateContent(xml, s.maxBytes); err != nil {
		return Info{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sn, err := s.scan()
	if err != nil {
		return Info{}, err
	}
	old := sn.find(folder, name)
	if old != nil {
		if ifUpdatedAt == nil {
			return Info{}, fmt.Errorf("a sample with that name %w", ErrExists)
		}
		if *ifUpdatedAt != old.UpdatedAt {
			return Info{}, &ConflictError{UpdatedAt: old.UpdatedAt}
		}
	}
	fd, err := s.resolveFolder(&sn, folder)
	if err != nil {
		return Info{}, err
	}
	if old != nil {
		fd = old.Folder
	}

	target := s.path(fd, fileName(name, tag))
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tmp-*")
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
		_ = os.Remove(s.path(old.Folder, old.file))
	}
	fi, err := os.Stat(target)
	if err != nil {
		return Info{}, err
	}
	return Info{fd, name, tag, fi.Size(), fi.ModTime().UnixMilli()}, nil
}

// Move renames, retags and/or moves a sample to another folder without
// touching its contents. ifUpdatedAt must match the sample's current version.
// The destination must not hold a different sample with that name.
func (s *Store) Move(folder, name string, to Target, ifUpdatedAt int64) (Info, error) {
	if err := validateTarget(to); err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sn, err := s.scan()
	if err != nil {
		return Info{}, err
	}
	src := sn.find(folder, name)
	if src == nil {
		return Info{}, fmt.Errorf("sample %w", ErrNotFound)
	}
	if src.UpdatedAt != ifUpdatedAt {
		return Info{}, &ConflictError{UpdatedAt: src.UpdatedAt}
	}
	if existing := sn.find(to.Folder, to.Name); existing != nil && existing != src {
		return Info{}, fmt.Errorf("a sample with that name %w in that folder", ErrExists)
	}
	fd, err := s.resolveFolder(&sn, to.Folder)
	if err != nil {
		return Info{}, err
	}
	from, dest := s.path(src.Folder, src.file), s.path(fd, fileName(to.Name, to.Tag))
	if from != dest {
		if err := os.Rename(from, dest); err != nil {
			return Info{}, err
		}
	}
	fi, err := os.Stat(dest)
	if err != nil {
		return Info{}, err
	}
	return Info{fd, to.Name, to.Tag, fi.Size(), fi.ModTime().UnixMilli()}, nil
}

func (s *Store) Delete(folder, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.scan()
	if err != nil {
		return err
	}
	e := sn.find(folder, name)
	if e == nil {
		return fmt.Errorf("sample %w", ErrNotFound)
	}
	return os.Remove(s.path(e.Folder, e.file))
}

// CreateFolder makes an empty folder.
func (s *Store) CreateFolder(name string) error {
	if name == "" {
		return fmt.Errorf("%w: a folder needs a name", ErrInvalid)
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.scan()
	if err != nil {
		return err
	}
	if _, ok := sn.folderDir(name); ok {
		return fmt.Errorf("a folder with that name %w", ErrExists)
	}
	if len(sn.folders) >= maxFolders {
		return fmt.Errorf("%w: too many folders", ErrInvalid)
	}
	return os.Mkdir(filepath.Join(s.dir, name), 0o700)
}

// RenameFolder renames a folder; its samples go with it. Changing only the
// letter case is allowed.
func (s *Store) RenameFolder(old, name string) error {
	if name == "" {
		return fmt.Errorf("%w: a folder needs a name", ErrInvalid)
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.scan()
	if err != nil {
		return err
	}
	actual, ok := sn.folderDir(old)
	if !ok {
		return fmt.Errorf("folder %w", ErrNotFound)
	}
	if other, exists := sn.folderDir(name); exists && !strings.EqualFold(other, actual) {
		return fmt.Errorf("a folder with that name %w", ErrExists)
	}
	if actual == name {
		return nil
	}
	return os.Rename(filepath.Join(s.dir, actual), filepath.Join(s.dir, name))
}

// DeleteFolder removes an empty folder. A folder with anything in it is
// ErrNotEmpty, so a click can never delete samples (or an admin's files).
func (s *Store) DeleteFolder(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.scan()
	if err != nil {
		return err
	}
	actual, ok := sn.folderDir(name)
	if !ok {
		return fmt.Errorf("folder %w", ErrNotFound)
	}
	if err := os.Remove(filepath.Join(s.dir, actual)); err != nil {
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return ErrNotEmpty
		}
		return err
	}
	return nil
}
