//go:build !darwin

// Package watch reports whether Zotero is running.
package watch

import "errors"

// ZoteroRunning is only implemented on macOS so far.
func ZoteroRunning() (bool, error) {
	return false, errors.New("detecting Zotero is not supported on this system")
}
