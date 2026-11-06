// Package receiver runs the connector server around Zotero's lifetime. Zotero
// tries to bind its port once at startup and gives up for the whole session if
// the port is taken, so the receiver has to get out of the way as soon as the
// Zotero process appears.
package receiver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/xin-yi6/zotty/internal/connector"
	"github.com/xin-yi6/zotty/internal/importer"
	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/watch"
	"github.com/xin-yi6/zotty/internal/zotapi"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const (
	pollInterval   = 500 * time.Millisecond
	zoteroBootWait = 2 * time.Minute
	importedMaxAge = 30 * 24 * time.Hour
)

// Config describes a receiver.
type Config struct {
	Port      int
	Store     *inbox.Store
	ZoteroDir string
	// DataDir holds changes waiting for Zotero. Empty means none are made.
	DataDir string
	Logger  *log.Logger
	// ZoteroRunning replaces process detection in tests.
	ZoteroRunning func() (bool, error)
	// Client replaces the client for the real Zotero in tests.
	Client *importer.Client
	// RetryInterval is the wait between import attempts.
	RetryInterval time.Duration
}

// Run alternates between receiving saves and standing aside for Zotero until
// ctx is canceled.
func Run(ctx context.Context, c Config) error {
	if c.ZoteroRunning == nil {
		c.ZoteroRunning = watch.ZoteroRunning
	}
	if c.Client == nil {
		c.Client = importer.NewClient(c.Port)
	}
	if c.RetryInterval == 0 {
		c.RetryInterval = 3 * time.Second
	}
	for ctx.Err() == nil {
		running, err := c.ZoteroRunning()
		if err != nil {
			return err
		}
		if running {
			if err := standAside(ctx, c); err != nil {
				return err
			}
			continue
		}
		if err := receive(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// receive serves the browser until Zotero starts or ctx is canceled.
func receive(ctx context.Context, c Config) error {
	listener, err := listen(ctx, c)
	if err != nil || listener == nil {
		return err
	}
	server := connector.New(c.Store, func() ([]zotdb.Target, []zotdb.Tag, error) {
		return zotdb.LoadTargets(c.ZoteroDir)
	}, c.Logger)
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	c.Logger.Printf("listening on %s", listener.Addr())

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return shutdown(httpServer)
		case err := <-serveErr:
			return err
		case <-ticker.C:
			running, err := c.ZoteroRunning()
			if err != nil {
				c.Logger.Printf("check for Zotero: %v", err)
				continue
			}
			if running {
				// Free the port first: an upload still in progress gets to
				// finish, but Zotero must not lose the race for the port.
				listener.Close()
				c.Logger.Printf("Zotero is starting, releasing the port")
				err := shutdown(httpServer)
				c.Logger.Printf("port released")
				return err
			}
		}
	}
}

// listen binds the port. Zotero can hold it for a moment after quitting, and
// the process check can't see that, so a busy port is retried briefly.
func listen(ctx context.Context, c Config) (net.Listener, error) {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port))
	for attempt := 0; ; attempt++ {
		// Loopback only: anything else would expose the inbox to the network.
		listener, err := net.Listen("tcp", address)
		if err == nil {
			return listener, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, err
		}
		if attempt == 20 {
			return nil, fmt.Errorf("port %d is in use by another program", c.Port)
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(pollInterval):
		}
	}
}

func shutdown(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return server.Shutdown(ctx)
}

// standAside imports the inbox once Zotero is ready, then waits for it to quit.
func standAside(ctx context.Context, c Config) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	finished, waiting := false, false
	var nextAttempt time.Time
	deadline := time.Now().Add(zoteroBootWait)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		running, err := c.ZoteroRunning()
		if err != nil {
			c.Logger.Printf("check for Zotero: %v", err)
			continue
		}
		if !running {
			c.Logger.Printf("Zotero quit")
			return nil
		}
		if finished || time.Now().Before(nextAttempt) {
			continue
		}
		// Zotero answers ping a little before it can save, and then fails
		// saves with a 500, so an early failure just means trying again.
		if err := c.Client.Ping(); err != nil {
			if !waiting {
				c.Logger.Printf("waiting for Zotero to answer: %v", err)
				waiting = true
			}
		} else if importInbox(c) {
			finished = true
			applyChanges(c)
			continue
		}
		if time.Now().After(deadline) {
			finished = true
			c.Logger.Printf("couldn't import the inbox. Restart Zotero to retry")
			continue
		}
		nextAttempt = time.Now().Add(c.RetryInterval)
	}
}

// importInbox reports whether every entry that can be imported was.
func importInbox(c Config) bool {
	result, err := importer.Run(c.Client, c.Store, zotdb.UserLibraryTarget(c.ZoteroDir), c.Logger.Printf)
	if err != nil {
		c.Logger.Printf("import: %v", err)
		return false
	}
	if result.Imported+result.Failed+result.Skipped > 0 {
		c.Logger.Printf("imported %d, failed %d, needs attention %d", result.Imported, result.Failed, result.Skipped)
	}
	if err := c.Store.PruneImported(importedMaxAge); err != nil {
		c.Logger.Printf("clean up imported files: %v", err)
	}
	return result.Failed == 0
}

// applyChanges makes the edits queued while Zotero was closed. If the local
// API is off they keep waiting, and zotty says why the next time it's used.
func applyChanges(c Config) {
	if c.DataDir == "" {
		return
	}
	queue := zotapi.OpenQueue(c.DataDir)
	if pending, err := queue.List(); err != nil || len(pending) == 0 {
		return
	}
	done, err := queue.Flush(zotapi.New(c.Port, c.DataDir), time.Now())
	if done > 0 {
		c.Logger.Printf("made %d changes saved while Zotero was closed", done)
	}
	if err != nil {
		c.Logger.Printf("changes still waiting: %v", err)
	}
}
