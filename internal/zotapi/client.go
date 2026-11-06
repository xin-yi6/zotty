// Package zotapi writes to Zotero through its local API (Zotero 10 and later).
// Zotero makes every change itself, so edits show up in its window at once and
// follow its own rules.
package zotapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xin-yi6/zotty/internal/connector"
)

var (
	// ErrNotRunning means nothing answered on Zotero's port.
	ErrNotRunning = errors.New("Zotero isn't running")
	// ErrDisabled means Zotero answered but its local API is off.
	ErrDisabled = errors.New(`turn on "Allow other applications on this computer to communicate with Zotero" in Zotero's Settings > Advanced`)
	// ErrDenied means the user clicked Deny in Zotero's dialog.
	ErrDenied = errors.New("access was denied in Zotero")
)

// Client is a local API client. Its key is stored in KeyFile when the user
// chose Always Allow, so Zotero only asks once.
type Client struct {
	BaseURL string
	KeyFile string
	HTTP    *http.Client

	serverID string
	key      string
	remember bool
}

// New returns a client for the Zotero listening on port.
func New(port int, dataDir string) *Client {
	return &Client{
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d/api/", port),
		KeyFile: filepath.Join(dataDir, "local-api-key.json"),
		// Authorizing waits for the user to answer Zotero's dialog.
		HTTP: &http.Client{Timeout: 3 * time.Minute},
	}
}

type storedKey struct {
	ServerID string `json:"serverID"`
	Key      string `json:"key"`
}

// Check reports whether Zotero is running with its local API on, without
// asking for access.
func (c *Client) Check() error {
	return c.connect()
}

// connect learns the server ID, which every write must send.
func (c *Client) connect() error {
	if c.serverID != "" {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(c.BaseURL)
	if err != nil {
		return ErrNotRunning
	}
	defer resp.Body.Close()
	if resp.Header.Get(connector.ReceiverHeader) != "" {
		// zotty's own receiver holds the port while Zotero is closed.
		return ErrNotRunning
	}
	if resp.StatusCode == http.StatusForbidden {
		return ErrDisabled
	}
	id := resp.Header.Get("Zotero-Server-ID")
	if id == "" {
		return fmt.Errorf("Zotero %s doesn't support writing through its local API. Update to Zotero 10 or later", resp.Header.Get("X-Zotero-Version"))
	}
	c.serverID = id
	return nil
}

// authorize gets a key, asking the user in Zotero if none is stored for this
// Zotero. Keys from another Zotero installation are ignored.
func (c *Client) authorize() error {
	if c.key != "" {
		return nil
	}
	if data, err := os.ReadFile(c.KeyFile); err == nil {
		var stored storedKey
		if json.Unmarshal(data, &stored) == nil && stored.ServerID == c.serverID && stored.Key != "" {
			c.key, c.remember = stored.Key, true
			return nil
		}
	}

	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"local/authorize", strings.NewReader(`{"appName":"zotty"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Zotero-Server-ID", c.serverID)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ask Zotero for access: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return ErrDenied
	case http.StatusTooManyRequests:
		return errors.New("Zotero asked too often. Try again in a minute")
	default:
		return responseError("authorize", resp)
	}
	var granted struct {
		Key      string `json:"key"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&granted); err != nil {
		return err
	}
	c.key, c.remember = granted.Key, granted.Remember
	if !granted.Remember {
		return nil
	}
	data, err := json.Marshal(storedKey{ServerID: c.serverID, Key: granted.Key})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.KeyFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(c.KeyFile, data, 0o600)
}

// Ready checks that writes can be made, asking the user for access if needed.
func (c *Client) Ready() error {
	if err := c.connect(); err != nil {
		return err
	}
	return c.authorize()
}

func (c *Client) do(method, path string, body any, headers map[string]string) (*http.Response, error) {
	if err := c.Ready(); err != nil {
		return nil, err
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Zotero-API-Version", "3")
		req.Header.Set("Zotero-Server-ID", c.serverID)
		req.Header.Set("Zotero-API-Key", c.key)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		// A one-time key is spent after one write, and a stored key may have
		// been cleared in Zotero's settings. Either way, ask again once.
		if c.key != "" && !c.remember {
			c.key = ""
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.key = ""
			os.Remove(c.KeyFile)
			if err := c.authorize(); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode == http.StatusPreconditionFailed && resp.Header.Get("Zotero-Server-ID") != "" && attempt == 0 {
			// A different Zotero data directory is open now.
			resp.Body.Close()
			c.serverID, c.key = "", ""
			if err := c.Ready(); err != nil {
				return nil, err
			}
			continue
		}
		return resp, nil
	}
}

func responseError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s: Zotero answered %d: %s", action, resp.StatusCode, strings.TrimSpace(string(body)))
}

// Item is the part of an item zotty changes, plus its version for safe updates.
type Item struct {
	Version     int
	Extra       string
	Tags        []Tag
	Collections []string
}

// Tag is a Zotero tag. Type 1 marks tags added automatically.
type Tag struct {
	Tag  string `json:"tag"`
	Type int    `json:"type,omitempty"`
}

// GetItem reads an item's current state.
func (c *Client) GetItem(key string) (Item, error) {
	if err := c.connect(); err != nil {
		return Item{}, err
	}
	resp, err := c.HTTP.Get(c.BaseURL + "users/0/items/" + key)
	if err != nil {
		return Item{}, ErrNotRunning
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return Item{}, ErrDisabled
	}
	if resp.StatusCode != http.StatusOK {
		return Item{}, responseError("read item", resp)
	}
	var body struct {
		Version int `json:"version"`
		Data    struct {
			Extra       string   `json:"extra"`
			Tags        []Tag    `json:"tags"`
			Collections []string `json:"collections"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Item{}, err
	}
	return Item{Version: body.Version, Extra: body.Data.Extra, Tags: body.Data.Tags, Collections: body.Data.Collections}, nil
}

// PatchItem changes some fields of an item. It fails rather than overwrite a
// change made since version was read.
func (c *Client) PatchItem(key string, version int, fields map[string]any) error {
	resp, err := c.do(http.MethodPatch, "users/0/items/"+key, fields, map[string]string{
		"If-Unmodified-Since-Version": strconv.Itoa(version),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return responseError("update item", resp)
	}
	return nil
}

// HasKey reports whether Zotero already granted zotty lasting access, so no
// dialog will appear.
func (c *Client) HasKey() bool {
	_, err := os.Stat(c.KeyFile)
	return err == nil
}
