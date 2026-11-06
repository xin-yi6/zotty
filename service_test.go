package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistIsValid(t *testing.T) {
	data, err := renderPlist("/Users/x/My Data/zotty", "/Users/x/Library/Logs/zotty.log")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v: %s", err, out)
	}
	if !strings.Contains(string(data), "/Users/x/My Data/zotty") {
		t.Errorf("plist = %s", data)
	}
}

func TestCopyExecutableReplacesOldBinary(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "new")
	to := filepath.Join(dir, "installed", "zotty")
	if err := os.WriteFile(from, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(from, to); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Errorf("content = %q", got)
	}
	info, err := os.Stat(to)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("mode = %v", info.Mode())
	}
}
