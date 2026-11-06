// Package inbox stores papers received from the browser until they can be
// imported into Zotero. Each entry is a directory holding entry.json and the
// entry's files, so a crash can lose at most the save in progress.
package inbox

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// ErrInvalidFile means a file's content does not match its declared type.
var ErrInvalidFile = errors.New("file content does not match its type")

var entryIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$`)

// Attachment is a file stored with an entry.
type Attachment struct {
	ID          string    `json:"id,omitempty"`
	Title       string    `json:"title"`
	URL         string    `json:"url,omitempty"`
	ContentType string    `json:"contentType"`
	File        string    `json:"file"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	SavedAt     time.Time `json:"savedAt"`
}

// Entry is one saved item. Item keeps the connector's JSON unchanged so the
// import step can hand Zotero exactly what the browser sent.
type Entry struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	CreatedAt    time.Time       `json:"createdAt"`
	ConnectorKey string          `json:"connectorKey,omitempty"`
	Title        string          `json:"title"`
	ItemType     string          `json:"itemType,omitempty"`
	URL          string          `json:"url,omitempty"`
	Target       string          `json:"target,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	Note         string          `json:"note,omitempty"`
	Item         json.RawMessage `json:"item,omitempty"`
	Attachments  []Attachment    `json:"attachments,omitempty"`
	// ImportError is set when an import stopped after the item reached Zotero.
	// Importing again would duplicate it, so the entry waits for the user.
	ImportError string `json:"importError,omitempty"`
}

// importedDir holds entries already in Zotero. They stay for a while as a safety
// copy, so a bad import can't lose a paper.
const importedDir = "imported"

// Store is a directory of entries. It is safe for concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open creates the inbox directory if needed. The inbox can hold subscription
// PDFs and notes, so it is private to the user.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Create assigns an ID and writes a new entry.
func (s *Store) Create(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := newID(time.Now())
	if err != nil {
		return Entry{}, err
	}
	e.ID = id
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if err := os.Mkdir(filepath.Join(s.dir, id), 0o700); err != nil {
		return Entry{}, err
	}
	return e, s.write(e)
}

// Update applies fn to a stored entry.
func (s *Store) Update(id string, fn func(*Entry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, err := s.read(id)
	if err != nil {
		return err
	}
	fn(&e)
	return s.write(e)
}

// AddFile streams r into the entry and records it as an attachment. Hashing
// and writing happen outside the lock so a large PDF doesn't block other saves.
func (s *Store) AddFile(id string, a Attachment, r io.Reader) (Attachment, error) {
	if !entryIDPattern.MatchString(id) {
		return Attachment{}, fmt.Errorf("invalid entry ID %q", id)
	}
	dir := filepath.Join(s.dir, id)
	tmp, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return Attachment{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	br := bufio.NewReader(r)
	if err := checkContent(a.ContentType, br); err != nil {
		return Attachment{}, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), br)
	if err != nil {
		return Attachment{}, err
	}
	if err := tmp.Close(); err != nil {
		return Attachment{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e, err := s.read(id)
	if err != nil {
		return Attachment{}, err
	}
	a.File = fmt.Sprintf("file-%d%s", len(e.Attachments)+1, extension(a.ContentType))
	a.Size = size
	a.SHA256 = hex.EncodeToString(hash.Sum(nil))
	a.SavedAt = time.Now().UTC()
	if err := os.Rename(tmp.Name(), filepath.Join(dir, a.File)); err != nil {
		return Attachment{}, err
	}
	e.Attachments = append(e.Attachments, a)
	return a, s.write(e)
}

// Delete removes an entry and its files.
func (s *Store) Delete(id string) error {
	if !entryIDPattern.MatchString(id) {
		return fmt.Errorf("invalid entry ID %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(filepath.Join(s.dir, id))
}

// Archive moves an imported entry out of the inbox.
func (s *Store) Archive(id string) error {
	if !entryIDPattern.MatchString(id) {
		return fmt.Errorf("invalid entry ID %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(s.dir, importedDir), 0o700); err != nil {
		return err
	}
	return os.Rename(filepath.Join(s.dir, id), filepath.Join(s.dir, importedDir, id))
}

// PruneImported removes archived entries older than age.
func (s *Store) PruneImported(age time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := filepath.Join(s.dir, importedDir)
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, d := range dirs {
		info, err := d.Info()
		if err != nil || !entryIDPattern.MatchString(d.Name()) || time.Since(info.ModTime()) < age {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, d.Name())); err != nil {
			return err
		}
	}
	return nil
}

// List returns all entries, newest first. An unreadable entry is skipped so
// one damaged directory can't hide the rest of the inbox.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dirs, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, d := range dirs {
		if !d.IsDir() || !entryIDPattern.MatchString(d.Name()) {
			continue
		}
		if e, err := s.read(d.Name()); err == nil {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID > entries[j].ID })
	return entries, nil
}

// Path returns the location of a file belonging to an entry.
func (s *Store) Path(id, file string) string {
	return filepath.Join(s.dir, id, file)
}

func (s *Store) read(id string) (Entry, error) {
	if !entryIDPattern.MatchString(id) {
		return Entry{}, fmt.Errorf("invalid entry ID %q", id)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, id, "entry.json"))
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// write replaces entry.json atomically.
func (s *Store) write(e Entry) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.dir, e.ID)
	tmp, err := os.CreateTemp(dir, "entry-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "entry.json"))
}

func newID(t time.Time) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return t.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b), nil
}

// checkContent rejects a login page saved under a PDF's name, which is what a
// failed publisher download usually produces.
func checkContent(contentType string, br *bufio.Reader) error {
	if contentType != "application/pdf" {
		return nil
	}
	head, err := br.Peek(1024)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return ErrInvalidFile
	}
	return nil
}

func extension(contentType string) string {
	switch contentType {
	case "application/pdf":
		return ".pdf"
	case "application/epub+zip":
		return ".epub"
	case "text/html":
		return ".html"
	default:
		return ".bin"
	}
}
