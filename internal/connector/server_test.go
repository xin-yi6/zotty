package connector

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const fakePDF = "%PDF-1.7\nfake paper\n%%EOF"

func newTestServer(t *testing.T) (*httptest.Server, *inbox.Store) {
	t.Helper()
	store, err := inbox.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	targets := func() ([]zotdb.Target, []zotdb.Tag, error) {
		return []zotdb.Target{
			{ID: "L1", Name: zotdb.LibraryName, FilesEditable: true},
			{ID: "C5", Name: "Holographic QCD", FilesEditable: true, Level: 1},
		}, []zotdb.Tag{{Tag: "QCD"}}, nil
	}
	server := New(store, targets, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

// call sends a request the way the extension does: from a browser, with its
// identifying headers.
func call(t *testing.T, ts *httptest.Server, path string, headers map[string]string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/connector/"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/140.0")
	req.Header.Set("Origin", "chrome-extension://ekhagklcjbdpajgpjgmbionohlpdbjgc")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Zotero-Version", "5.0.0")
	req.Header.Set("X-Zotero-Connector-API-Version", "3")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: status %d, want %d: %s", resp.Request.URL.Path, resp.StatusCode, want, body)
	}
}

func oneEntry(t *testing.T, store *inbox.Store) inbox.Entry {
	t.Helper()
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	return entries[0]
}

func TestPingLooksLikeZotero(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := call(t, ts, "ping", nil, []byte(`{}`))
	wantStatus(t, resp, http.StatusOK)

	if resp.Header.Get("X-Zotero-Version") == "" {
		t.Error("missing X-Zotero-Version")
	}
	var body struct {
		Prefs map[string]any `json:"prefs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Prefs["supportsAttachmentUpload"] != true {
		t.Errorf("prefs = %v", body.Prefs)
	}
	if _, ok := body.Prefs["translatorsHash"]; ok {
		t.Error("translatorsHash would make the extension fetch translators from us")
	}
}

// TestSaveItemWithPDF replays the order of calls in itemSaver.js for a paper.
func TestSaveItemWithPDF(t *testing.T) {
	ts, store := newTestServer(t)

	wantStatus(t, call(t, ts, "saveItems", nil, []byte(`{
		"sessionID": "abc",
		"uri": "https://arxiv.org/abs/2611.00001",
		"items": [{
			"id": "item1",
			"itemType": "preprint",
			"title": "Holographic QCD",
			"creators": [{"firstName": "A", "lastName": "B", "creatorType": "author"}],
			"url": "https://arxiv.org/abs/2611.00001",
			"attachments": []
		}]
	}`)), http.StatusCreated)

	resp := call(t, ts, "getSelectedCollection", nil, []byte(`{}`))
	wantStatus(t, resp, http.StatusOK)
	var selected struct {
		FilesEditable bool `json:"filesEditable"`
		Targets       []struct {
			ID string `json:"id"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&selected); err != nil {
		t.Fatal(err)
	}
	if !selected.FilesEditable || len(selected.Targets) != 2 {
		t.Fatalf("selected = %+v", selected)
	}

	metadata := `{"id":"att1","url":"https://arxiv.org/pdf/2611.00001","contentType":"application/pdf","parentItemID":"item1","title":"=?utf-8?B?5YWo5paH?= PDF"}`
	wantStatus(t, call(t, ts, "saveAttachment?sessionID=abc", map[string]string{
		"Content-Type": "application/pdf",
		"X-Metadata":   metadata,
	}, []byte(fakePDF)), http.StatusCreated)

	wantStatus(t, call(t, ts, "updateSession", nil, []byte(
		`{"sessionID":"abc","target":"C5","tags":["QCD","to-read"],"note":"check section 3"}`,
	)), http.StatusOK)

	e := oneEntry(t, store)
	if e.Title != "Holographic QCD" || e.ItemType != "preprint" {
		t.Errorf("entry = %+v", e)
	}
	if e.Target != "C5" || len(e.Tags) != 2 || e.Note != "check section 3" {
		t.Errorf("popup choices not saved: %+v", e)
	}
	if !strings.Contains(string(e.Item), `"creators"`) {
		t.Error("raw item was not kept for import")
	}
	if len(e.Attachments) != 1 {
		t.Fatalf("attachments = %+v", e.Attachments)
	}
	a := e.Attachments[0]
	if a.Title != "全文 PDF" {
		t.Errorf("title = %q", a.Title)
	}
	got, err := os.ReadFile(store.Path(e.ID, a.File))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fakePDF {
		t.Errorf("file = %q", got)
	}
}

func TestSaveSeveralItems(t *testing.T) {
	ts, store := newTestServer(t)
	wantStatus(t, call(t, ts, "saveItems", nil, []byte(`{"sessionID":"abc","uri":"https://example.org",
		"items":[{"id":"a","title":"One"},{"id":"b","title":"Two"}]}`)), http.StatusCreated)
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
}

func TestStandalonePDF(t *testing.T) {
	ts, store := newTestServer(t)
	resp := call(t, ts, "saveStandaloneAttachment?sessionID=abc", map[string]string{
		"Content-Type": "application/pdf",
		"X-Metadata":   `{"url":"https://example.org/p.pdf","title":"p.pdf"}`,
	}, []byte(fakePDF))
	wantStatus(t, resp, http.StatusCreated)

	var body map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["canRecognize"] {
		t.Error("the extension would wait for a recognition we don't do")
	}
	if e := oneEntry(t, store); len(e.Attachments) != 1 {
		t.Errorf("attachments = %+v", e.Attachments)
	}
}

func TestFailedDownloadIsNotSavedAsPDF(t *testing.T) {
	ts, store := newTestServer(t)
	resp := call(t, ts, "saveStandaloneAttachment?sessionID=abc", map[string]string{
		"Content-Type": "application/pdf",
		"X-Metadata":   `{"url":"https://example.org/p.pdf","title":"p.pdf"}`,
	}, []byte("<html>Please sign in</html>"))
	wantStatus(t, resp, http.StatusBadRequest)

	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("left an empty entry behind: %+v", entries)
	}
}

func TestSnapshotDropsCookies(t *testing.T) {
	ts, store := newTestServer(t)
	wantStatus(t, call(t, ts, "saveSnapshot", nil, []byte(`{"sessionID":"abc","url":"https://example.org/a",
		"title":"A page","cookie":"session=secret","detailedCookies":"secret"}`)), http.StatusCreated)
	wantStatus(t, call(t, ts, "saveSingleFile", nil, []byte(`{"sessionID":"abc","url":"https://example.org/a",
		"title":"A page","snapshotContent":"<html>hi</html>"}`)), http.StatusCreated)

	e := oneEntry(t, store)
	if len(e.Attachments) != 1 {
		t.Fatalf("attachments = %+v", e.Attachments)
	}
	data, err := os.ReadFile(store.Path(e.ID, "entry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Errorf("cookies reached the disk: %s", data)
	}
}

func TestDuplicateSession(t *testing.T) {
	ts, _ := newTestServer(t)
	body := []byte(`{"sessionID":"abc","items":[{"id":"a","title":"One"}]}`)
	wantStatus(t, call(t, ts, "saveItems", nil, body), http.StatusCreated)
	wantStatus(t, call(t, ts, "saveItems", nil, body), http.StatusConflict)
}

// An error without the version header would read as "Zotero is offline".
func TestErrorsIdentifyAsZotero(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"updateSession", "noSuchEndpoint"} {
		resp := call(t, ts, path, nil, []byte(`{"sessionID":"missing"}`))
		if resp.StatusCode < 400 || resp.Header.Get("X-Zotero-Version") == "" {
			t.Errorf("%s: status %d, version %q", path, resp.StatusCode, resp.Header.Get("X-Zotero-Version"))
		}
	}
}

func TestWebsitesCannotSave(t *testing.T) {
	ts, store := newTestServer(t)
	body := []byte(`{"sessionID":"abc","items":[{"id":"a","title":"Injected"}]}`)

	// A page can POST to localhost, but can't add the extension's header
	// without a preflight, and preflights get no CORS headers.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/connector/saveItems", bytes.NewReader(body))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wantStatus(t, resp, http.StatusForbidden)

	req, _ = http.NewRequest(http.MethodOptions, ts.URL+"/connector/saveItems", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Headers", "x-zotero-connector-api-version")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("preflight allowed a website")
	}

	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("website saved an entry: %+v", entries)
	}
}

func TestRebindingHostRejected(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/connector/ping", strings.NewReader(`{}`))
	req.Host = "evil.example"
	req.Header.Set("X-Zotero-Connector-API-Version", "3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wantStatus(t, resp, http.StatusBadRequest)
}

func TestSessionIDNeverBecomesAPath(t *testing.T) {
	ts, store := newTestServer(t)
	wantStatus(t, call(t, ts, "saveItems", nil, []byte(
		`{"sessionID":"../../escape","items":[{"id":"../x","title":"One"}]}`,
	)), http.StatusCreated)
	e := oneEntry(t, store)
	wantStatus(t, call(t, ts, "saveAttachment?sessionID=../../escape", map[string]string{
		"Content-Type": "application/pdf",
		"X-Metadata":   `{"parentItemID":"../x","title":"../../evil.pdf"}`,
	}, []byte(fakePDF)), http.StatusCreated)

	entries, _ := store.List()
	if got := entries[0].Attachments[0].File; got != "file-1.pdf" {
		t.Errorf("file = %q (entry %s)", got, e.ID)
	}
}
