package zotapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

// fakeZotero imitates the local API for one item.
type fakeZotero struct {
	mu          sync.Mutex
	disabled    bool
	remember    bool
	authorized  int
	version     int
	extra       string
	tags        []Tag
	collections []string
	keys        map[string]bool
	patches     []map[string]any
}

func newFake() *fakeZotero {
	return &fakeZotero{version: 5, extra: "arXiv:2611.00001\nRead_Status: New\nRead_Status_Date: 2026-11-01T00:00:00.000Z",
		tags: []Tag{{Tag: "auto", Type: 1}, {Tag: "QCD"}}, collections: []string{"AAAA1111"}, keys: map[string]bool{}, remember: true}
}

func (f *fakeZotero) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Zotero-Server-ID", "server1")
	if f.disabled {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/api/":
		return
	case r.URL.Path == "/api/local/authorize":
		if r.Header.Get("Zotero-Server-ID") != "server1" {
			w.WriteHeader(http.StatusPreconditionRequired)
			return
		}
		f.authorized++
		key := strings.Repeat("k", 31) + string(rune('a'+f.authorized))
		f.keys[key] = true
		json.NewEncoder(w).Encode(map[string]any{"key": key, "remember": f.remember})
	case r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{"version": f.version, "data": map[string]any{
			"extra": f.extra, "tags": f.tags, "collections": f.collections,
		}})
	case r.Method == http.MethodPatch:
		key := r.Header.Get("Zotero-API-Key")
		if !f.keys[key] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !f.remember {
			delete(f.keys, key)
		}
		if r.Header.Get("If-Unmodified-Since-Version") != "5" && r.Header.Get("If-Unmodified-Since-Version") != "6" {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var fields map[string]any
		json.Unmarshal(body, &fields)
		f.patches = append(f.patches, fields)
		if extra, ok := fields["extra"].(string); ok {
			f.extra = extra
		}
		f.version++
		w.WriteHeader(http.StatusNoContent)
	}
}

func setup(t *testing.T) (*fakeZotero, *Client, string) {
	t.Helper()
	fake := newFake()
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	c := &Client{BaseURL: ts.URL + "/api/", KeyFile: filepath.Join(dir, "key.json"), HTTP: ts.Client()}
	return fake, c, dir
}

func TestWithStatusKeepsOtherLines(t *testing.T) {
	got := WithStatus("arXiv:1\nRead_Status: New\nRead_Status_Date: x\nNote: keep", "Read", now)
	want := "arXiv:1\nNote: keep\nRead_Status: Read\nRead_Status_Date: 2026-11-02T08:00:00.000Z"
	if got != want {
		t.Errorf("got %q", got)
	}
	if got := WithStatus("", "To Read", now); !strings.HasPrefix(got, "Read_Status: To Read\n") {
		t.Errorf("empty extra: %q", got)
	}
}

func TestSetStatusAsksOnceAndRemembers(t *testing.T) {
	fake, c, _ := setup(t)
	if err := c.Apply(Change{ItemKey: "ITEM", Kind: SetStatus, Value: "In Progress"}, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.extra, "Read_Status: In Progress") || !strings.HasPrefix(fake.extra, "arXiv:2611.00001") {
		t.Errorf("extra = %q", fake.extra)
	}

	// A new client, as in the next zotty command, reuses the stored key.
	again := &Client{BaseURL: c.BaseURL, KeyFile: c.KeyFile, HTTP: c.HTTP}
	if err := again.Apply(Change{ItemKey: "ITEM", Kind: SetStatus, Value: "Read"}, now); err != nil {
		t.Fatal(err)
	}
	if fake.authorized != 1 {
		t.Errorf("asked %d times, want 1", fake.authorized)
	}
}

func TestOneTimeKeysAreNotStored(t *testing.T) {
	fake, c, _ := setup(t)
	fake.remember = false
	for _, status := range []string{"Read", "New"} {
		if err := c.Apply(Change{ItemKey: "ITEM", Kind: SetStatus, Value: status}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(c.KeyFile); err == nil {
		t.Error("stored a one-time key")
	}
	if fake.authorized != 2 {
		t.Errorf("asked %d times, want once per write", fake.authorized)
	}
}

func TestClearedKeyIsReplaced(t *testing.T) {
	fake, c, _ := setup(t)
	os.WriteFile(c.KeyFile, []byte(`{"serverID":"server1","key":"revoked"}`), 0o600)
	if err := c.Apply(Change{ItemKey: "ITEM", Kind: AddTag, Value: "x"}, now); err != nil {
		t.Fatal(err)
	}
	if fake.authorized != 1 {
		t.Errorf("authorized %d times", fake.authorized)
	}
}

func TestTagsKeepAutomaticOnes(t *testing.T) {
	fake, c, _ := setup(t)
	if err := c.Apply(Change{ItemKey: "ITEM", Kind: AddTag, Value: "to-cite"}, now); err != nil {
		t.Fatal(err)
	}
	tags, _ := json.Marshal(fake.patches[0]["tags"])
	if string(tags) != `[{"tag":"auto","type":1},{"tag":"QCD"},{"tag":"to-cite"}]` {
		t.Errorf("tags = %s", tags)
	}
	if err := c.Apply(Change{ItemKey: "ITEM", Kind: RemoveTag, Value: "QCD"}, now); err != nil {
		t.Fatal(err)
	}
	tags, _ = json.Marshal(fake.patches[1]["tags"])
	if string(tags) != `[{"tag":"auto","type":1}]` {
		t.Errorf("tags = %s", tags)
	}
}

func TestCollectionsAddWithoutDuplicates(t *testing.T) {
	fake, c, _ := setup(t)
	if err := c.Apply(Change{ItemKey: "ITEM", Kind: AddCollection, Value: "AAAA1111"}, now); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(fake.patches[0]["collections"])
	if string(got) != `["AAAA1111"]` {
		t.Errorf("collections = %s", got)
	}
}

func TestDisabledAPIExplainsTheSetting(t *testing.T) {
	fake, c, _ := setup(t)
	fake.disabled = true
	err := c.Apply(Change{ItemKey: "ITEM", Kind: SetStatus, Value: "Read"}, now)
	if err != ErrDisabled {
		t.Errorf("err = %v", err)
	}
}

func TestReceiverMeansZoteroIsClosed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Zotty-Receiver", "1")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	c := &Client{BaseURL: ts.URL + "/api/", KeyFile: filepath.Join(t.TempDir(), "k"), HTTP: ts.Client()}
	q := OpenQueue(t.TempDir())
	queued, err := Submit(c, q, Change{ItemKey: "ITEM", Kind: SetStatus, Value: "Read"}, now)
	if err != nil || !queued {
		t.Errorf("queued=%v err=%v", queued, err)
	}
}

func TestQueueWhileClosedThenFlush(t *testing.T) {
	_, c, dir := setup(t)
	closed := &Client{BaseURL: "http://127.0.0.1:1/api/", KeyFile: c.KeyFile, HTTP: c.HTTP}
	q := OpenQueue(dir)
	for _, status := range []string{"In Progress", "Read"} {
		queued, err := Submit(closed, q, Change{ItemKey: "ITEM", Kind: SetStatus, Value: status}, now)
		if err != nil || !queued {
			t.Fatalf("queued=%v err=%v", queued, err)
		}
	}
	done, err := q.Flush(c, now)
	if err != nil || done != 2 {
		t.Fatalf("done=%d err=%v", done, err)
	}
	if pending, _ := q.List(); len(pending) != 0 {
		t.Errorf("left %d changes", len(pending))
	}
}

func TestFlushStopsAtFirstFailureAndKeepsOrder(t *testing.T) {
	fake, c, dir := setup(t)
	q := OpenQueue(dir)
	q.Add(Change{ItemKey: "ITEM", Kind: SetStatus, Value: "Read"})
	q.Add(Change{ItemKey: "ITEM", Kind: "bogus"})
	q.Add(Change{ItemKey: "ITEM", Kind: SetStatus, Value: "New"})
	done, err := q.Flush(c, now)
	if err == nil || done != 1 {
		t.Fatalf("done=%d err=%v", done, err)
	}
	pending, _ := q.List()
	if len(pending) != 2 || pending[0].Kind != "bogus" {
		t.Errorf("pending = %+v", pending)
	}
	if !strings.Contains(fake.extra, "Read_Status: Read") {
		t.Errorf("first change missing: %q", fake.extra)
	}
}

func TestQueuedStatusKeepsItsTime(t *testing.T) {
	fake, c, _ := setup(t)
	made := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := c.Apply(Change{ItemKey: "ITEM", Kind: SetStatus, Value: "Read", Created: made}, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.extra, "Read_Status_Date: 2026-10-30T09:00:00.000Z") {
		t.Errorf("extra = %q", fake.extra)
	}
}
