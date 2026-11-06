package zotdb

import (
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/xin-yi6/zotty/internal/watch"
)

// snapshot is an open read-only view of zotero.sqlite.
type snapshot struct {
	*sql.DB
	cleanup func()
}

func (s *snapshot) Close() error {
	err := s.DB.Close()
	s.cleanup()
	return err
}

// openLibrary opens zotero.sqlite without ever writing to it. With Zotero closed
// the file can be read in place. While Zotero runs it holds an exclusive lock
// and recent changes sit in the WAL, so the database and WAL are copied first.
func openLibrary(dataDir string) (*snapshot, error) {
	path := filepath.Join(dataDir, "zotero.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no Zotero library at %s: %w", dataDir, err)
	}
	if !needsCopy(path) {
		db, err := sql.Open("sqlite", fileDSN(path, "mode=ro&immutable=1"))
		if err != nil {
			return nil, err
		}
		return &snapshot{DB: db, cleanup: func() {}}, nil
	}

	dir, err := os.MkdirTemp("", "zotty-snapshot-")
	if err != nil {
		return nil, err
	}
	remove := func() { os.RemoveAll(dir) }
	copied := filepath.Join(dir, "zotero.sqlite")
	if err := copyFile(path, copied); err != nil {
		remove()
		return nil, err
	}
	if err := copyFile(path+"-wal", copied+"-wal"); err != nil && !os.IsNotExist(err) {
		remove()
		return nil, err
	}
	db, err := sql.Open("sqlite", fileDSN(copied, ""))
	if err != nil {
		remove()
		return nil, err
	}
	return &snapshot{DB: db, cleanup: remove}, nil
}

func needsCopy(path string) bool {
	if running, err := watch.ZoteroRunning(); err != nil || running {
		return true
	}
	info, err := os.Stat(path + "-wal")
	return err == nil && info.Size() > 0
}

func fileDSN(path, query string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String()
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// Stamp changes whenever Zotero writes to the library, so a reader can tell
// when to load it again without opening it.
func Stamp(dataDir string) string {
	path := filepath.Join(dataDir, "zotero.sqlite")
	stamp := ""
	for _, p := range []string{path, path + "-wal"} {
		if info, err := os.Stat(p); err == nil {
			stamp += fmt.Sprintf("%d.%d;", info.Size(), info.ModTime().UnixNano())
		}
	}
	return stamp
}
