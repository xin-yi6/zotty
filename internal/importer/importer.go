// Package importer moves inbox entries into a running Zotero by replaying the
// calls the browser extension would have made. Zotero then does all the writing
// itself, so zotero.sqlite is never touched from here.
package importer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/xin-yi6/zotty/internal/connector"
	"github.com/xin-yi6/zotty/internal/inbox"
)

// ErrNotZotero means nothing that identifies as Zotero answered.
var ErrNotZotero = errors.New("Zotero is not reachable")

// Client talks to Zotero's connector server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a Client for a Zotero listening on port.
func NewClient(port int) *Client {
	return &Client{
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d/connector/", port),
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// Ping reports whether the real Zotero is answering, not zotty's own receiver.
func (c *Client) Ping() error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(c.BaseURL + "ping")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotZotero, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Zotero-Version") == "" ||
		resp.Header.Get(connector.ReceiverHeader) != "" {
		return ErrNotZotero
	}
	return nil
}

// Result summarizes an import run.
type Result struct {
	Imported int
	Failed   int
	Skipped  int
}

// Run imports every entry oldest first. defaultTarget is used for saves where
// the popup's choice wasn't recorded. A failure is logged and the run moves on,
// because one bad entry shouldn't hold back the rest.
func Run(c *Client, store *inbox.Store, defaultTarget string, logf func(string, ...any)) (Result, error) {
	var result Result
	if err := c.Ping(); err != nil {
		return result, err
	}
	entries, err := store.List()
	if err != nil {
		return result, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.ImportError != "" {
			result.Skipped++
			continue
		}
		if err := importEntry(c, store, e, defaultTarget); err != nil {
			result.Failed++
			logf("import %q: %v", e.Title, err)
			continue
		}
		if err := store.Archive(e.ID); err != nil {
			return result, err
		}
		result.Imported++
		logf("imported %q", e.Title)
	}
	return result, nil
}

// importEntry saves one entry. Once the item exists in Zotero a retry would
// create a duplicate, so a later failure is recorded on the entry instead of
// leaving it to be retried automatically.
func importEntry(c *Client, store *inbox.Store, e inbox.Entry, defaultTarget string) error {
	sessionID, err := newSessionID()
	if err != nil {
		return err
	}

	files := e.Attachments
	switch e.Kind {
	case "saveItems":
		err = c.postJSON("saveItems", map[string]any{
			"sessionID": sessionID,
			"uri":       e.URL,
			"items":     []json.RawMessage{e.Item},
		})
	case "saveSnapshot":
		err = c.postJSON("saveSnapshot", map[string]any{
			"sessionID": sessionID, "url": e.URL, "title": e.Title,
		})
	case "saveStandaloneAttachment":
		if len(files) == 0 {
			return errors.New("entry has no file")
		}
		err = c.postFile("saveStandaloneAttachment", sessionID, store.Path(e.ID, files[0].File), files[0], "")
		files = files[1:]
	default:
		return fmt.Errorf("unknown entry kind %q", e.Kind)
	}
	if err != nil {
		return err
	}

	if err := finishEntry(c, store, e, files, sessionID, defaultTarget); err != nil {
		if markErr := store.Update(e.ID, func(stored *inbox.Entry) {
			stored.ImportError = err.Error()
		}); markErr != nil {
			return errors.Join(err, markErr)
		}
		return err
	}
	return nil
}

func finishEntry(c *Client, store *inbox.Store, e inbox.Entry, files []inbox.Attachment, sessionID, defaultTarget string) error {
	for _, a := range files {
		var err error
		if a.ContentType == "text/html" {
			err = c.postSnapshot(store.Path(e.ID, a.File), e, a, sessionID)
		} else {
			err = c.postFile("saveAttachment", sessionID, store.Path(e.ID, a.File), a, e.ConnectorKey)
		}
		if err != nil {
			return fmt.Errorf("attachment %q: %w", a.Title, err)
		}
	}
	target := e.Target
	if target == "" {
		target = defaultTarget
	}
	return c.postJSON("updateSession", map[string]any{
		"sessionID": sessionID, "target": target, "tags": e.Tags, "note": e.Note,
	})
}

func (c *Client) postSnapshot(path string, e inbox.Entry, a inbox.Attachment, sessionID string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return c.postJSON("saveSingleFile", map[string]any{
		"sessionID":       sessionID,
		"snapshotContent": string(content),
		"url":             a.URL,
		"title":           a.Title,
		"items":           []map[string]string{{"id": e.ConnectorKey}},
	})
}

func (c *Client) postJSON(method string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(method, req)
}

func (c *Client) postFile(method, sessionID, path string, a inbox.Attachment, parentKey string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	metadata, err := asciiJSON(map[string]string{
		"id":           a.ID,
		"url":          a.URL,
		"contentType":  a.ContentType,
		"parentItemID": parentKey,
		"title":        a.Title,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+method+"?sessionID="+sessionID, file)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", a.ContentType)
	req.Header.Set("X-Metadata", metadata)
	return c.do(method, req)
}

func (c *Client) do(method string, req *http.Request) error {
	req.Header.Set("X-Zotero-Connector-API-Version", "3")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: status %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// asciiJSON encodes v using \u escapes, because header values must be ASCII.
// Unlike RFC 2047 this can't break the JSON when a title contains a quote.
func asciiJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range string(data) {
		if r < 0x80 {
			b.WriteRune(r)
			continue
		}
		for _, unit := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&b, `\u%04x`, unit)
		}
	}
	return b.String(), nil
}

func newSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
