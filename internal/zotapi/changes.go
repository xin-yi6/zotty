package zotapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Change is one edit to an item. Edits wait in a queue while Zotero is closed
// and are applied, in order, once it opens.
type Change struct {
	ItemKey string    `json:"itemKey"`
	Title   string    `json:"title,omitempty"`
	Kind    string    `json:"kind"`
	Value   string    `json:"value"`
	Created time.Time `json:"created"`
}

// Kinds of change.
const (
	SetStatus        = "status"
	AddTag           = "addTag"
	RemoveTag        = "removeTag"
	AddCollection    = "addCollection"
	RemoveCollection = "removeCollection"
)

// Statuses are the reading states of the Reading List plugin, in the order
// the interface cycles through them.
var Statuses = []string{"To Read", "In Progress", "Read", "New"}

var (
	statusLine     = regexp.MustCompile(`(?m)^Read_Status: .*\n?`)
	statusDateLine = regexp.MustCompile(`(?m)^Read_Status_Date: .*\n?`)
)

// WithStatus rewrites the Extra field the way the Reading List plugin does: a
// Read_Status line and a Read_Status_Date line, keeping everything else.
func WithStatus(extra, status string, now time.Time) string {
	extra = statusLine.ReplaceAllString(extra, "")
	extra = statusDateLine.ReplaceAllString(extra, "")
	extra = strings.TrimRight(extra, "\n")
	lines := fmt.Sprintf("Read_Status: %s\nRead_Status_Date: %s", status, now.UTC().Format("2006-01-02T15:04:05.000Z"))
	if extra == "" {
		return lines
	}
	return extra + "\n" + lines
}

// Apply makes one change in Zotero, reading the item first so nothing written
// since is lost.
func (c *Client) Apply(ch Change, now time.Time) error {
	item, err := c.GetItem(ch.ItemKey)
	if err != nil {
		return err
	}
	fields := map[string]any{}
	switch ch.Kind {
	case SetStatus:
		// A queued change keeps the time it was made, not when Zotero opened.
		when := now
		if !ch.Created.IsZero() {
			when = ch.Created
		}
		fields["extra"] = WithStatus(item.Extra, ch.Value, when)
	case AddTag, RemoveTag:
		tags := slices.DeleteFunc(slices.Clone(item.Tags), func(t Tag) bool { return t.Tag == ch.Value })
		if ch.Kind == AddTag {
			tags = append(tags, Tag{Tag: ch.Value})
		}
		fields["tags"] = tags
	case AddCollection, RemoveCollection:
		collections := slices.DeleteFunc(slices.Clone(item.Collections), func(k string) bool { return k == ch.Value })
		if ch.Kind == AddCollection {
			collections = append(collections, ch.Value)
		}
		fields["collections"] = collections
	default:
		return fmt.Errorf("unknown change %q", ch.Kind)
	}
	return c.PatchItem(ch.ItemKey, item.Version, fields)
}

// Queue is the list of changes waiting for Zotero, kept in a file.
type Queue struct {
	path string
	mu   sync.Mutex
}

// OpenQueue returns the queue stored in dataDir.
func OpenQueue(dataDir string) *Queue {
	return &Queue{path: filepath.Join(dataDir, "pending-changes.json")}
}

// List returns the waiting changes, oldest first.
func (q *Queue) List() ([]Change, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.read()
}

// Add appends a change.
func (q *Queue) Add(ch Change) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	changes, err := q.read()
	if err != nil {
		return err
	}
	return q.write(append(changes, ch))
}

// Flush applies the waiting changes in order. It stops at the first that
// fails, so later edits never land before earlier ones.
func (q *Queue) Flush(c *Client, now time.Time) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	changes, err := q.read()
	if err != nil || len(changes) == 0 {
		return 0, err
	}
	done := 0
	for _, ch := range changes {
		if err := c.Apply(ch, now); err != nil {
			if writeErr := q.write(changes[done:]); writeErr != nil {
				return done, errors.Join(err, writeErr)
			}
			return done, err
		}
		done++
	}
	return done, q.write(nil)
}

func (q *Queue) read() ([]Change, error) {
	data, err := os.ReadFile(q.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var changes []Change
	if err := json.Unmarshal(data, &changes); err != nil {
		return nil, fmt.Errorf("read %s: %w", q.path, err)
	}
	return changes, nil
}

func (q *Queue) write(changes []Change) error {
	if len(changes) == 0 {
		err := os.Remove(q.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0o700); err != nil {
		return err
	}
	tmp := q.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}

// Submit makes a change now if Zotero can take it, or queues it if Zotero is
// closed. It reports whether the change was queued.
func Submit(c *Client, q *Queue, ch Change, now time.Time) (bool, error) {
	if pending, err := q.List(); err == nil && len(pending) > 0 {
		// Keep order: earlier queued edits go first.
		if _, err := q.Flush(c, now); err != nil && !errors.Is(err, ErrNotRunning) {
			return false, err
		}
	}
	err := c.Apply(ch, now)
	if errors.Is(err, ErrNotRunning) {
		return true, q.Add(ch)
	}
	return false, err
}
