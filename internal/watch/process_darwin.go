// Package watch reports whether Zotero is running.
package watch

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// ZoteroRunning reports whether a Zotero process exists. It asks the kernel
// directly because the receiver calls it twice a second.
func ZoteroRunning() (bool, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return false, err
	}
	name := []byte("zotero")
	for i := range procs {
		comm := procs[i].Proc.P_comm[:]
		if end := bytes.IndexByte(comm, 0); end >= 0 {
			comm = comm[:end]
		}
		if bytes.Equal(comm, name) {
			return true, nil
		}
	}
	return false, nil
}
