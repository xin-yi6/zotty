package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xin-yi6/zotty/internal/adder"
	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotapi"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

// listFlag collects a flag given more than once, as in --tag a --tag b.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

var statusNames = map[string]string{
	"to-read": "To Read", "toread": "To Read",
	"in-progress": "In Progress", "inprogress": "In Progress", "reading": "In Progress",
	"read": "Read", "new": "New",
}

func parseStatus(s string) (string, error) {
	key := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "-"))
	if status, ok := statusNames[key]; ok {
		return status, nil
	}
	return "", fmt.Errorf("unknown state %q. Use to-read, in-progress, read, or new", s)
}

// submit makes a change now, or queues it for when Zotero opens.
func submit(p *paths, port int, ch zotapi.Change) error {
	client := zotapi.New(port, p.data)
	if _, err := os.Stat(client.KeyFile); err != nil {
		fmt.Println("If Zotero asks, allow zotty to make changes. Choose Always Allow to be asked only once.")
	}
	queued, err := zotapi.Submit(client, zotapi.OpenQueue(p.data), ch, time.Now())
	if err != nil {
		return err
	}
	if queued {
		fmt.Println("Zotero is closed, so this will be made the next time it opens")
		return nil
	}
	fmt.Println("Updated in Zotero")
	return nil
}

func loadRef(p *paths, ref string) (zotdb.Item, error) {
	items, err := zotdb.LoadItems(p.zotero)
	if err != nil {
		return zotdb.Item{}, err
	}
	return resolveRef(p, items, ref)
}

func runRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	p := addPathFlags(fs)
	words := parseArgs(fs, args)
	if len(words) != 2 {
		return errors.New("usage: zotty read REF STATE, where STATE is to-read, in-progress, read, or new")
	}
	status, err := parseStatus(words[1])
	if err != nil {
		return err
	}
	it, err := loadRef(p, words[0])
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", it.Title, status)
	return submit(p, p.port, zotapi.Change{ItemKey: it.Key, Title: it.Title, Kind: zotapi.SetStatus, Value: status, Created: time.Now()})
}

func runTag(args []string, remove bool) error {
	name, kind := "tag", zotapi.AddTag
	if remove {
		name, kind = "untag", zotapi.RemoveTag
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	p := addPathFlags(fs)
	words := parseArgs(fs, args)
	if len(words) < 2 {
		return fmt.Errorf("usage: zotty %s REF TAG...", name)
	}
	it, err := loadRef(p, words[0])
	if err != nil {
		return err
	}
	for _, tag := range words[1:] {
		ch := zotapi.Change{ItemKey: it.Key, Title: it.Title, Kind: kind, Value: tag, Created: time.Now()}
		if err := submit(p, p.port, ch); err != nil {
			return err
		}
	}
	return nil
}

func runMove(args []string) error {
	fs := flag.NewFlagSet("move", flag.ExitOnError)
	p := addPathFlags(fs)
	remove := fs.Bool("remove", false, "take the paper out of the collection instead")
	words := parseArgs(fs, args)
	if len(words) < 2 {
		return errors.New(`usage: zotty move REF COLLECTION, as in zotty move 3 "QCD theory / Phase structure"`)
	}
	it, err := loadRef(p, words[0])
	if err != nil {
		return err
	}
	collections, err := zotdb.LoadCollections(p.zotero)
	if err != nil {
		return err
	}
	target, err := zotdb.FindCollection(collections, strings.Join(words[1:], " "))
	if err != nil {
		return err
	}
	kind := zotapi.AddCollection
	if *remove {
		kind = zotapi.RemoveCollection
	}
	fmt.Printf("%s: %s\n", it.Title, zotdb.CollectionPath(collections, target.ID))
	return submit(p, p.port, zotapi.Change{ItemKey: it.Key, Title: it.Title, Kind: kind, Value: target.Key, Created: time.Now()})
}

// runAdd saves a paper by arXiv ID or DOI.
func runAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	p := addPathFlags(fs)
	to := fs.String("to", "", "collection to add the paper to")
	force := fs.Bool("force", false, "add even if the paper is already in your library")
	var tags listFlag
	fs.Var(&tags, "tag", "tag to add, can be repeated")
	words := parseArgs(fs, args)
	if len(words) != 1 {
		return errors.New("usage: zotty add ARXIV-ID-OR-DOI [--to COLLECTION] [--tag TAG]")
	}
	target := ""
	if *to != "" {
		collections, err := zotdb.LoadCollections(p.zotero)
		if err != nil {
			return err
		}
		c, err := zotdb.FindCollection(collections, *to)
		if err != nil {
			return err
		}
		target = fmt.Sprintf("C%d", c.ID)
	}
	store, err := inbox.Open(filepath.Join(p.data, "inbox"))
	if err != nil {
		return err
	}
	result, err := adder.Add(words[0], adder.Options{
		ZoteroDir: p.zotero, Port: p.port, Store: store, Target: target, Tags: tags, Force: *force,
	})
	if result.Title != "" {
		fmt.Println(result.Title)
	}
	if dup, ok := err.(adder.ErrDuplicate); ok {
		return fmt.Errorf("%v. Use --force to add it again", dup)
	}
	if err != nil {
		return err
	}
	if result.PDFError != nil {
		fmt.Printf("Saved without a PDF: %v\n", result.PDFError)
	} else {
		fmt.Println("PDF downloaded")
	}
	if result.Imported {
		fmt.Println("Added to Zotero")
	} else {
		fmt.Println("Zotero is closed, so the paper is in the inbox and goes in when Zotero opens")
	}
	return nil
}
