package receiver

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xin-yi6/zotty/internal/importer"
	"github.com/xin-yi6/zotty/internal/inbox"
)

// freePort finds a free port below the range the system hands out to other
// tests' servers, so none of them can take it between this check and the
// receiver binding it.
func freePort(t *testing.T) int {
	t.Helper()
	for range 100 {
		port := 20000 + rand.Intn(10000)
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			l.Close()
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logBuffer collects the receiver's log to show when a test fails.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func TestReleasesPortImportsAndComesBack(t *testing.T) {
	// The first save fails the way a Zotero that is still starting does.
	var saves atomic.Int32
	zotero := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Zotero-Version", "10.0.6")
		if r.Method == http.MethodPost {
			if r.URL.Path == "/connector/saveItems" && saves.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer zotero.Close()

	store, err := inbox.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	var running atomic.Bool
	logs := &logBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("saves: %d\nreceiver log:\n%s", saves.Load(), logs)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Port:          port,
			Store:         store,
			ZoteroDir:     t.TempDir(),
			Logger:        log.New(logs, "", log.Lmicroseconds),
			ZoteroRunning: func() (bool, error) { return running.Load(), nil },
			Client:        &importer.Client{BaseURL: zotero.URL + "/connector/", HTTP: zotero.Client()},
			RetryInterval: 50 * time.Millisecond,
		})
	}()

	waitFor(t, "receiver to listen", func() bool { return portOpen(port) })
	body := []byte(`{"sessionID":"s","items":[{"id":"a","title":"One"}]}`)
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/connector/saveItems", port), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save answered %d, want 201", resp.StatusCode)
	}
	if entries, _ := store.List(); len(entries) != 1 {
		t.Fatalf("inbox has %d entries after the save, want 1", len(entries))
	}

	running.Store(true)
	waitFor(t, "port to be released", func() bool { return !portOpen(port) })
	waitFor(t, "import retry", func() bool { return saves.Load() == 2 })
	waitFor(t, "inbox to empty", func() bool {
		entries, _ := store.List()
		return len(entries) == 0
	})

	running.Store(false)
	waitFor(t, "receiver to return", func() bool { return portOpen(port) })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
