// Package connector answers the Zotero Connector browser extension on Zotero's
// local port, so the save button keeps working while Zotero is closed.
//
// The protocol is not a public API. Behavior here follows Zotero's
// server_connector.js and the extension's connector.js and itemSaver.js.
package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const (
	apiVersion = "3"
	// The extension treats an error without this header as "Zotero is offline".
	clientVersion = "10.0.0"

	// ReceiverHeader marks responses from zotty, so zotty never mistakes its own
	// receiver for the real Zotero.
	ReceiverHeader = "X-Zotty-Receiver"

	maxJSONBody = 256 << 20
	maxFileBody = 1 << 30
	sessionTTL  = time.Hour
)

var hostPattern = regexp.MustCompile(`(?i)^(127\.0\.0\.1|\[::1\]|localhost)(:[0-9]+)?$`)

// TargetLoader lists the collections and tags the browser popup shows.
type TargetLoader func() ([]zotdb.Target, []zotdb.Tag, error)

// Server handles connector requests. Sessions live in memory: they only need to
// outlast one save, and the extension starts a new one each time.
type Server struct {
	store   *inbox.Store
	targets TargetLoader
	logger  *log.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	createdAt time.Time
	entryIDs  []string
	keys      map[string]string
}

// New returns a Server that saves into store.
func New(store *inbox.Store, targets TargetLoader, logger *log.Logger) *Server {
	return &Server{store: store, targets: targets, logger: logger, sessions: map[string]*session{}}
}

// Handler returns the HTTP handler for the connector endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/connector/ping", s.ping)
	mux.HandleFunc("/connector/getSelectedCollection", s.selectedCollection)
	mux.HandleFunc("/connector/saveItems", s.saveItems)
	mux.HandleFunc("/connector/saveSnapshot", s.saveSnapshot)
	mux.HandleFunc("/connector/saveAttachment", s.saveAttachment)
	mux.HandleFunc("/connector/saveStandaloneAttachment", s.saveStandaloneAttachment)
	mux.HandleFunc("/connector/saveSingleFile", s.saveSingleFile)
	mux.HandleFunc("/connector/updateSession", s.updateSession)
	mux.HandleFunc("/connector/hasAttachmentResolvers", respondJSON(http.StatusOK, false))
	mux.HandleFunc("/connector/getTranslators", respondJSON(http.StatusOK, []any{}))
	mux.HandleFunc("/connector/getClientHostnames", respondJSON(http.StatusOK, []any{}))
	mux.HandleFunc("/connector/proxies", respondJSON(http.StatusOK, []any{}))
	mux.HandleFunc("/connector/getRecognizedItem", respondStatus(http.StatusNoContent))
	mux.HandleFunc("/connector/delaySync", respondStatus(http.StatusNoContent))
	return guard(mux)
}

// guard keeps websites away from the save endpoints. Any page can send requests
// to localhost, so like Zotero it rejects DNS rebinding through the Host header
// and browser requests that lack the extension's header. Preflights get no CORS
// headers, which stops a page from adding that header itself.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Zotero-Version", clientVersion)
		w.Header().Set("X-Zotero-Connector-API-Version", apiVersion)
		w.Header().Set(ReceiverHeader, "1")

		if !hostPattern.MatchString(r.Host) {
			http.Error(w, "Invalid Host header", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodOptions || r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		isBrowser := strings.HasPrefix(r.UserAgent(), "Mozilla/") || r.Header.Get("Origin") != ""
		hasToken := r.Header.Get("X-Zotero-Connector-API-Version") != "" || r.Header.Get("Zotero-Allowed-Request") != ""
		navigation := r.URL.Path == "/connector/ping" && r.Header.Get("Sec-Fetch-Mode") == "navigate"
		if isBrowser && !hasToken && !navigation {
			http.Error(w, "Request from browser not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<!DOCTYPE html><html><body>Zotero is running</body></html>")
		return
	}
	// translatorsHash is left out on purpose: the extension would otherwise try
	// to replace its translators with ours, and we have none.
	writeJSON(w, http.StatusOK, map[string]any{"prefs": map[string]any{
		"automaticSnapshots":       false,
		"downloadAssociatedFiles":  true,
		"supportsAttachmentUpload": true,
		"supportsTagsAutocomplete": true,
		"canUserAddNote":           true,
	}})
}

func (s *Server) selectedCollection(w http.ResponseWriter, r *http.Request) {
	targets, tags, err := s.targets()
	if err != nil {
		// Saving still works without the collection list.
		s.logger.Printf("read collections: %v", err)
		targets = []zotdb.Target{{ID: "L1", Name: zotdb.LibraryName, FilesEditable: true}}
		tags = []zotdb.Tag{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"libraryID":       1,
		"libraryName":     zotdb.LibraryName,
		"libraryEditable": true,
		"filesEditable":   true,
		"editable":        true,
		"id":              nil,
		"name":            zotdb.LibraryName,
		"targets":         targets,
		"tags":            map[string]any{targets[0].ID: tags},
	})
}

func (s *Server) saveItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string            `json:"sessionID"`
		URI       string            `json:"uri"`
		Items     []json.RawMessage `json:"items"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "SESSION_ID_NOT_PROVIDED")
		return
	}
	sess, ok := s.newSession(req.SessionID)
	if !ok {
		writeError(w, http.StatusConflict, "SESSION_EXISTS")
		return
	}
	for _, raw := range req.Items {
		var item struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			ItemType string `json:"itemType"`
			URL      string `json:"url"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ITEM")
			return
		}
		url := item.URL
		if url == "" {
			url = req.URI
		}
		entry, err := s.store.Create(inbox.Entry{
			Kind:         "saveItems",
			ConnectorKey: item.ID,
			Title:        item.Title,
			ItemType:     item.ItemType,
			URL:          url,
			Item:         raw,
		})
		if err != nil {
			s.fail(w, "save item", err)
			return
		}
		s.register(sess, item.ID, entry.ID)
		s.logger.Printf("saved %q", item.Title)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
}

// saveSnapshot handles pages with no translator. The request can also carry the
// user's cookies; only the fields needed later are kept.
func (s *Server) saveSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionID"`
		URL       string `json:"url"`
		URI       string `json:"uri"`
		Title     string `json:"title"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "SESSION_ID_NOT_PROVIDED")
		return
	}
	sess, ok := s.newSession(req.SessionID)
	if !ok {
		writeError(w, http.StatusConflict, "SESSION_EXISTS")
		return
	}
	url := req.URL
	if url == "" {
		url = req.URI
	}
	entry, err := s.store.Create(inbox.Entry{
		Kind: "saveSnapshot", ConnectorKey: url, Title: req.Title, ItemType: "webpage", URL: url,
	})
	if err != nil {
		s.fail(w, "save page", err)
		return
	}
	s.register(sess, url, entry.ID)
	s.logger.Printf("saved page %q", req.Title)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
}

// attachmentMetadata is the X-Metadata header of an attachment upload.
type attachmentMetadata struct {
	ID           string `json:"id"`
	SessionID    string `json:"sessionID"`
	URL          string `json:"url"`
	Title        string `json:"title"`
	ParentItemID string `json:"parentItemID"`
}

func parseMetadata(r *http.Request) (attachmentMetadata, error) {
	var m attachmentMetadata
	header := r.Header.Get("X-Metadata")
	if header == "" {
		return m, errors.New("METADATA_NOT_PROVIDED")
	}
	if err := json.Unmarshal([]byte(header), &m); err != nil {
		return m, errors.New("INVALID_METADATA")
	}
	// The extension RFC 2047-encodes titles because headers are ASCII-only.
	if title, err := new(mime.WordDecoder).DecodeHeader(m.Title); err == nil {
		m.Title = title
	}
	if m.SessionID == "" {
		m.SessionID = r.URL.Query().Get("sessionID")
	}
	if m.SessionID == "" {
		return m, errors.New("SESSION_ID_NOT_PROVIDED")
	}
	return m, nil
}

func (s *Server) saveAttachment(w http.ResponseWriter, r *http.Request) {
	m, err := parseMetadata(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entryID, ok := s.lookup(m.SessionID, m.ParentItemID)
	if !ok {
		writeError(w, http.StatusBadRequest, "SESSION_NOT_FOUND")
		return
	}
	if !s.storeFile(w, r, entryID, m) {
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// saveStandaloneAttachment handles a PDF opened directly in the browser.
func (s *Server) saveStandaloneAttachment(w http.ResponseWriter, r *http.Request) {
	m, err := parseMetadata(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, ok := s.newSession(m.SessionID)
	if !ok {
		writeError(w, http.StatusConflict, "SESSION_EXISTS")
		return
	}
	entry, err := s.store.Create(inbox.Entry{
		Kind: "saveStandaloneAttachment", ConnectorKey: m.URL, Title: m.Title, URL: m.URL,
	})
	if err != nil {
		s.fail(w, "save attachment", err)
		return
	}
	s.register(sess, m.URL, entry.ID)
	if !s.storeFile(w, r, entry.ID, m) {
		// The entry exists only to hold this file.
		if err := s.store.Delete(entry.ID); err != nil {
			s.logger.Printf("remove empty entry: %v", err)
		}
		return
	}
	s.logger.Printf("saved file %q", m.Title)
	writeJSON(w, http.StatusCreated, map[string]bool{"canRecognize": false})
}

func (s *Server) storeFile(w http.ResponseWriter, r *http.Request, entryID string, m attachmentMetadata) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		contentType = "application/octet-stream"
	}
	body := http.MaxBytesReader(w, r.Body, maxFileBody)
	a, err := s.store.AddFile(entryID, inbox.Attachment{
		ID: m.ID, Title: m.Title, URL: m.URL, ContentType: contentType,
	}, body)
	if errors.Is(err, inbox.ErrInvalidFile) {
		writeError(w, http.StatusBadRequest, "INVALID_FILE")
		return false
	}
	if err != nil {
		s.fail(w, "save file", err)
		return false
	}
	s.logger.Printf("stored %s (%d bytes)", a.Title, a.Size)
	return true
}

func (s *Server) saveSingleFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID       string `json:"sessionID"`
		SnapshotContent string `json:"snapshotContent"`
		URL             string `json:"url"`
		Title           string `json:"title"`
		Items           []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	key := req.URL
	if len(req.Items) > 0 {
		key = req.Items[0].ID
	}
	entryID, ok := s.lookup(req.SessionID, key)
	if !ok {
		writeError(w, http.StatusBadRequest, "SESSION_NOT_FOUND")
		return
	}
	if req.SnapshotContent != "" {
		_, err := s.store.AddFile(entryID, inbox.Attachment{
			Title: req.Title, URL: req.URL, ContentType: "text/html",
		}, strings.NewReader(req.SnapshotContent))
		if err != nil {
			s.fail(w, "save snapshot", err)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
}

// updateSession records the collection, tags, and note chosen in the popup.
func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string          `json:"sessionID"`
		Target    string          `json:"target"`
		Tags      json.RawMessage `json:"tags"`
		Note      string          `json:"note"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "SESSION_ID_NOT_PROVIDED")
		return
	}
	s.mu.Lock()
	sess, ok := s.sessions[req.SessionID]
	var ids []string
	if ok {
		ids = append(ids, sess.entryIDs...)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusBadRequest, "SESSION_NOT_FOUND")
		return
	}
	tags := parseTags(req.Tags)
	for _, id := range ids {
		err := s.store.Update(id, func(e *inbox.Entry) {
			e.Target = req.Target
			e.Tags = tags
			e.Note = req.Note
		})
		if err != nil {
			s.fail(w, "update session", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// parseTags accepts both forms: older extensions send one comma-separated string.
func parseTags(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var joined string
	if json.Unmarshal(raw, &joined) == nil && joined != "" {
		return strings.Split(joined, ",")
	}
	return nil
}

func (s *Server) newSession(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, sess := range s.sessions {
		if time.Since(sess.createdAt) > sessionTTL {
			delete(s.sessions, key)
		}
	}
	if _, exists := s.sessions[id]; exists {
		return nil, false
	}
	sess := &session{createdAt: time.Now(), keys: map[string]string{}}
	s.sessions[id] = sess
	return sess, true
}

func (s *Server) register(sess *session, key, entryID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.keys[key] = entryID
	sess.entryIDs = append(sess.entryIDs, entryID)
}

func (s *Server) lookup(sessionID, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return "", false
	}
	id, ok := sess.keys[key]
	return id, ok
}

func (s *Server) fail(w http.ResponseWriter, action string, err error) {
	s.logger.Printf("%s: %v", action, err)
	writeError(w, http.StatusInternalServerError, "SAVE_FAILED")
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusBadRequest, "POST_REQUIRED")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("INVALID_JSON: %v", err))
		return false
	}
	return true
}

func respondJSON(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, status, v) }
}

func respondStatus(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
