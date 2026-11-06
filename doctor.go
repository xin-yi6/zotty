package main

import (
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/watch"
	"github.com/xin-yi6/zotty/internal/zotapi"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

// version is set when a release is built. Builds from `go install` fall back
// to the module version.
var version = ""

func currentVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// runDoctor checks everything zotty depends on and says how to fix what isn't
// right. It changes nothing.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	p := addPathFlags(fs)
	fs.Parse(args)
	problems := 0
	ok := func(text string) { fmt.Println("  ✓ " + text) }
	note := func(text string) { fmt.Println("  · " + text) }
	fail := func(text, fix string) {
		problems++
		fmt.Println("  ✗ " + text)
		fmt.Println("    " + fix)
	}

	fmt.Printf("zotty %s\n\n", currentVersion())

	if items, err := zotdb.LoadItems(p.zotero); err != nil {
		fail("Can't read the Zotero library: "+err.Error(),
			"Open Zotero once to create it, or pass --zotero-dir if it lives somewhere else")
	} else {
		ok(fmt.Sprintf("Zotero library at %s, %d papers", p.zotero, len(items)))
	}

	switch v := zoteroAppVersion(); {
	case v == "":
		fail("Zotero isn't in /Applications", "Install it from https://www.zotero.org/download/")
	case majorVersion(v) < 10:
		fail("Zotero "+v+" can't take changes from zotty", "Update to Zotero 10 or later. Browsing and Chrome saves still work")
	default:
		ok("Zotero " + v)
	}

	if running, _ := watch.ZoteroRunning(); running {
		err := zotapi.New(p.port, p.data).Check()
		switch {
		case errors.Is(err, zotapi.ErrDisabled):
			fail("Zotero is running but doesn't let other apps make changes", "In Zotero's Settings > Advanced, "+err.Error())
		case err != nil:
			fail("Zotero is running but its local API didn't answer: "+err.Error(), "Restart Zotero and run zotty doctor again")
		default:
			ok("Zotero is running and accepts changes")
		}
	} else {
		note("Zotero is closed. Changes and Chrome saves wait until it opens")
	}

	installed := filepath.Join(p.data, "bin", "zotty")
	switch {
	case exec.Command("launchctl", "print", launchTarget()+"/"+serviceLabel).Run() != nil:
		fail("The receiver isn't running, so Chrome can't save while Zotero is closed", "Run zotty service install")
	default:
		out, _ := exec.Command(installed, "version").Output()
		if got := strings.TrimSpace(string(out)); got != currentVersion() {
			fail("The receiver runs zotty "+got+", not "+currentVersion(), "Run zotty service install to update it")
		} else {
			ok("The receiver is running and starts at login")
		}
	}

	if store, err := inbox.Open(filepath.Join(p.data, "inbox")); err == nil {
		if entries, err := store.List(); err == nil && len(entries) > 0 {
			note(fmt.Sprintf("%d papers in the inbox wait for Zotero to open", len(entries)))
		}
	}

	if _, err := exec.LookPath("zotty"); err != nil {
		fail("zotty isn't on your PATH", "Link it there, as in: ln -s "+installed+" /usr/local/bin/zotty")
	}

	fmt.Println()
	if problems == 0 {
		fmt.Println("Everything looks good")
		return nil
	}
	return fmt.Errorf("found %d problem%s", problems, map[bool]string{true: "s"}[problems > 1])
}

// zoteroAppVersion reads the installed Zotero's version, or returns "".
func zoteroAppVersion() string {
	out, err := exec.Command("plutil", "-extract", "CFBundleShortVersionString", "raw",
		"/Applications/Zotero.app/Contents/Info.plist").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func majorVersion(v string) int {
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}
