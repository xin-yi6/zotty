// Command zotty is a lightweight companion to Zotero.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/xin-yi6/zotty/internal/importer"
	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/receiver"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const usage = `zotty - a lightweight companion to Zotero

Usage:
  zotty                    Browse your library
  zotty search QUERY       Search by title, author, year, tag, or collection
                           Fields: a:author t:title y:year s:status c:collection #tag
  zotty show REF           Show a paper's details, notes, and highlights
  zotty open REF           Open a paper's PDF
  zotty queue [GROUP]      List papers by reading state:
                           to-read, in-progress, new, or read
  zotty grep WORDS         Find PDFs whose text contains the words

  zotty add ID             Add a paper by arXiv ID or DOI, with its arXiv PDF
                           [--to COLLECTION] [--tag TAG]
  zotty read REF STATE     Set the reading state: to-read, in-progress, read, new
  zotty tag REF TAG...     Add tags. zotty untag removes them
  zotty move REF COLL      Put a paper in a collection, or take it out with --remove

  Changes go through Zotero, which asks once for permission. While Zotero is
  closed they wait and are made when it opens.

  zotty serve              Receive saves from Chrome while Zotero is closed,
                           and import them when Zotero opens
  zotty inbox [--json]     List papers waiting to be imported into Zotero
  zotty import             Import the inbox into a running Zotero now
  zotty service install    Run the receiver now and at every login
  zotty service uninstall  Stop the receiver and remove it from login
  zotty doctor             Check Zotero, the receiver, and your setup
  zotty version            Print the version

REF is a number from the last search, queue, or grep, or a Zotero item key.

Shared flags:
  --data-dir PATH        Where zotty keeps its inbox
  --zotero-dir PATH      Zotero's data directory (default: from Zotero's settings)
  --port N               Zotero's port (default 23119)
`

func main() {
	args := os.Args[1:]
	// Flags alone, as in `zotty --zotero-dir PATH`, open the interface.
	if len(args) == 0 || (strings.HasPrefix(args[0], "-") && !isHelpOrVersion(args[0])) {
		args = append([]string{"tui"}, args...)
	}
	var err error
	switch args[0] {
	case "tui":
		err = runTUI(args[1:])
	case "search":
		err = runSearch(args[1:])
	case "show":
		err = runShow(args[1:])
	case "open":
		err = runOpen(args[1:])
	case "queue":
		err = runQueue(args[1:])
	case "grep":
		err = runGrep(args[1:])
	case "serve":
		err = runServe(args[1:])
	case "inbox":
		err = runInbox(args[1:])
	case "import":
		err = runImport(args[1:])
	case "service":
		err = runService(args[1:])
	case "add":
		err = runAdd(args[1:])
	case "read":
		err = runRead(args[1:])
	case "tag":
		err = runTag(args[1:], false)
	case "untag":
		err = runTag(args[1:], true)
	case "move":
		err = runMove(args[1:])
	case "doctor":
		err = runDoctor(args[1:])
	case "version", "--version", "-v":
		fmt.Println(currentVersion())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "zotty: unknown command %q\n\n%s", args[0], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "zotty: %v\n", err)
		os.Exit(1)
	}
}

func isHelpOrVersion(arg string) bool {
	switch arg {
	case "-h", "--help", "-v", "--version":
		return true
	}
	return false
}

type paths struct {
	data, zotero string
	// port is where Zotero listens, and where the receiver stands in for it.
	port int
}

func addPathFlags(fs *flag.FlagSet) *paths {
	home, _ := os.UserHomeDir()
	config, _ := os.UserConfigDir()
	p := &paths{}
	fs.StringVar(&p.data, "data-dir", filepath.Join(config, "zotty"), "where zotty keeps its inbox")
	fs.StringVar(&p.zotero, "zotero-dir", zotdb.DefaultDir(home), "Zotero's data directory")
	fs.IntVar(&p.port, "port", 23119, "port Zotero listens on")
	return p
}

func runInbox(args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	p := addPathFlags(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)

	store, err := inbox.Open(filepath.Join(p.data, "inbox"))
	if err != nil {
		return err
	}
	entries, err := store.List()
	if err != nil {
		return err
	}
	if *asJSON {
		out := json.NewEncoder(os.Stdout)
		out.SetIndent("", "  ")
		return out.Encode(entries)
	}
	if len(entries) == 0 {
		fmt.Println("Inbox is empty")
		return nil
	}
	for i, e := range entries {
		fmt.Printf("%2d  %s  %s\n", i+1, e.CreatedAt.Local().Format("Jan 02 15:04"), e.Title)
		if e.ImportError != "" {
			fmt.Printf("    Import stopped, check this paper in Zotero: %s\n", e.ImportError)
		}
		for _, a := range e.Attachments {
			fmt.Printf("    %s\n", store.Path(e.ID, a.File))
		}
	}
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	p := addPathFlags(fs)
	fs.Parse(args)

	store, err := inbox.Open(filepath.Join(p.data, "inbox"))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return receiver.Run(ctx, receiver.Config{
		Port:      p.port,
		Store:     store,
		ZoteroDir: p.zotero,
		DataDir:   p.data,
		Logger:    log.New(os.Stdout, "", log.Ltime),
	})
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	p := addPathFlags(fs)
	fs.Parse(args)

	store, err := inbox.Open(filepath.Join(p.data, "inbox"))
	if err != nil {
		return err
	}
	result, err := importer.Run(importer.NewClient(p.port), store, zotdb.UserLibraryTarget(p.zotero),
		func(format string, a ...any) { fmt.Printf(format+"\n", a...) })
	if err != nil {
		return fmt.Errorf("%w. Open Zotero and try again", err)
	}
	fmt.Printf("Imported %d, failed %d, needs attention %d\n", result.Imported, result.Failed, result.Skipped)
	return nil
}
