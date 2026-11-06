package importer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/xin-yi6/zotty/internal/connector"
	"github.com/xin-yi6/zotty/internal/inbox"
)

const fakePDF = "%PDF-1.7\nfake paper\n%%EOF"

type call struct {
	path     string
	metadata string
	session  string
	body     string
}

// fakeZotero records calls and can fail one endpoint.
type fakeZotero struct {
	mu     sync.Mutex
	calls  []call
	failOn string
}

func (f *fakeZotero) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Zotero-Version", "10.0.6")
	if r.Method == http.MethodGet {
		return
	}
	body, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/connector/")
	f.mu.Lock()
	f.calls = append(f.calls, call{path, r.Header.Get("X-Metadata"), r.URL.Query().Get("sessionID"), string(body)})
	f.mu.Unlock()
	if path == f.failOn {
		http.Error(w, "nope", http.StatusInternalServerError)
		return
	}
	if path == "updateSession" {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (f *fakeZotero) paths() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.path)
	}
	return out
}

func setup(t *testing.T) (*fakeZotero, *Client, *inbox.Store) {
	t.Helper()
	fake := &fakeZotero{}
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	store, err := inbox.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fake, &Client{BaseURL: ts.URL + "/connector/", HTTP: ts.Client()}, store
}

func addPaper(t *testing.T, store *inbox.Store) inbox.Entry {
	t.Helper()
	e, err := store.Create(inbox.Entry{
		Kind: "saveItems", ConnectorKey: "item1", Title: "A paper", URL: "https://arxiv.org/abs/1",
		Target: "C246", Tags: []string{"QCD"}, Note: "read me",
		Item: json.RawMessage(`{"id":"item1","itemType":"preprint","title":"A paper","attachments":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddFile(e.ID, inbox.Attachment{
		ID: "a1", Title: `全文 "PDF"`, URL: "https://arxiv.org/pdf/1", ContentType: "application/pdf",
	}, strings.NewReader(fakePDF))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func logfDiscard(string, ...any) {}

func TestImportReplaysSave(t *testing.T) {
	fake, client, store := setup(t)
	addPaper(t, store)

	result, err := Run(client, store, "L1", logfDiscard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	got := strings.Join(fake.paths(), " ")
	if got != "saveItems saveAttachment updateSession" {
		t.Fatalf("calls = %s", got)
	}

	var saved struct {
		SessionID string `json:"sessionID"`
		Items     []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(fake.calls[0].body), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Items[0].ID != "item1" {
		t.Errorf("saved = %+v", saved)
	}

	attachment := fake.calls[1]
	if attachment.session != saved.SessionID || attachment.body != fakePDF {
		t.Errorf("attachment = %+v", attachment)
	}
	for _, r := range attachment.metadata {
		if r > 0x7f {
			t.Fatalf("X-Metadata is not ASCII: %s", attachment.metadata)
		}
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(attachment.metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["title"] != `全文 "PDF"` || metadata["parentItemID"] != "item1" {
		t.Errorf("metadata = %+v", metadata)
	}

	var update map[string]any
	if err := json.Unmarshal([]byte(fake.calls[2].body), &update); err != nil {
		t.Fatal(err)
	}
	if update["target"] != "C246" || update["note"] != "read me" {
		t.Errorf("update = %+v", update)
	}

	if entries, _ := store.List(); len(entries) != 0 {
		t.Errorf("inbox not emptied: %+v", entries)
	}
}

func TestImportStandalonePDF(t *testing.T) {
	fake, client, store := setup(t)
	e, err := store.Create(inbox.Entry{Kind: "saveStandaloneAttachment", Title: "p.pdf", URL: "https://x/p.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFile(e.ID, inbox.Attachment{Title: "p.pdf", ContentType: "application/pdf"}, strings.NewReader(fakePDF)); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(client, store, "L1", logfDiscard); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.paths(), " "); got != "saveStandaloneAttachment updateSession" {
		t.Errorf("calls = %s", got)
	}
}

func TestFailureBeforeItemExistsCanRetry(t *testing.T) {
	fake, client, store := setup(t)
	addPaper(t, store)
	fake.failOn = "saveItems"

	result, err := Run(client, store, "L1", logfDiscard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}

	fake.failOn = ""
	result, err = Run(client, store, "L1", logfDiscard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("retry result = %+v", result)
	}
}

func TestFailureAfterItemExistsIsNotRetried(t *testing.T) {
	fake, client, store := setup(t)
	addPaper(t, store)
	fake.failOn = "saveAttachment"

	if _, err := Run(client, store, "L1", logfDiscard); err != nil {
		t.Fatal(err)
	}
	entries, _ := store.List()
	if len(entries) != 1 || entries[0].ImportError == "" {
		t.Fatalf("entries = %+v", entries)
	}

	fake.failOn = ""
	before := len(fake.calls)
	result, err := Run(client, store, "L1", logfDiscard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped != 1 || len(fake.calls) != before {
		t.Errorf("retried and would duplicate: %+v", result)
	}
}

func TestPingRejectsOwnReceiver(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Zotero-Version", "10.0.0")
		w.Header().Set(connector.ReceiverHeader, "1")
	}))
	defer ts.Close()
	client := &Client{BaseURL: ts.URL + "/connector/", HTTP: ts.Client()}
	if err := client.Ping(); err == nil {
		t.Error("treated zotty's receiver as Zotero")
	}
}
