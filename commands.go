package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/tui"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const defaultLimit = 20

func printJSON(v any) error {
	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)
	out.SetIndent("", "  ")
	return out.Encode(v)
}

// parseArgs lets flags appear after the words of a command, as in
// `zotty search holographic chiral --limit 5`, which flag.Parse alone stops at.
func parseArgs(fs *flag.FlagSet, args []string) []string {
	var words []string
	for len(args) > 0 {
		fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			words = append(words, args[0])
			args = args[1:]
		}
	}
	return words
}

// rememberResults saves the keys of the items just listed so `zotty open 3`
// means the third row on screen.
func rememberResults(p *paths, items []zotdb.Item) error {
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.Key
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.data, "last-results.json"), data, 0o600)
}

func resolveRef(p *paths, items []zotdb.Item, ref string) (zotdb.Item, error) {
	key := ref
	if n, err := strconv.Atoi(ref); err == nil {
		data, err := os.ReadFile(filepath.Join(p.data, "last-results.json"))
		if err != nil {
			return zotdb.Item{}, errors.New("no recent results. Run zotty search first")
		}
		var keys []string
		if err := json.Unmarshal(data, &keys); err != nil {
			return zotdb.Item{}, err
		}
		if n < 1 || n > len(keys) {
			return zotdb.Item{}, fmt.Errorf("no result %d in the last list of %d", n, len(keys))
		}
		key = keys[n-1]
	}
	for _, it := range items {
		if strings.EqualFold(it.Key, key) {
			return it, nil
		}
	}
	return zotdb.Item{}, fmt.Errorf("no item %q in your library", ref)
}

func printList(items []zotdb.Item) {
	for i, it := range items {
		fmt.Printf("%3d  %-14s %-4s  %s\n", i+1, clip(it.FirstAuthor(), 14), it.Year, it.Title)
	}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func limited(items []zotdb.Item, limit int) []zotdb.Item {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func runSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	p := addPathFlags(fs)
	limit := fs.Int("limit", defaultLimit, "maximum results, 0 for all")
	asJSON := fs.Bool("json", false, "print JSON")
	words := parseArgs(fs, args)
	if len(words) == 0 {
		return errors.New("usage: zotty search QUERY")
	}

	items, err := zotdb.LoadItems(p.zotero)
	if err != nil {
		return err
	}
	matches := zotdb.Filter(items, strings.Join(words, " "))
	shown := limited(matches, *limit)
	if err := rememberResults(p, shown); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(shown)
	}
	if len(matches) == 0 {
		fmt.Println("No matches")
		return nil
	}
	printList(shown)
	if len(shown) < len(matches) {
		fmt.Printf("Showing %d of %d matches\n", len(shown), len(matches))
	}
	return nil
}

func runQueue(args []string) error {
	fs := flag.NewFlagSet("queue", flag.ExitOnError)
	p := addPathFlags(fs)
	limit := fs.Int("limit", defaultLimit, "maximum results, 0 for all")
	asJSON := fs.Bool("json", false, "print JSON")
	words := parseArgs(fs, args)

	group := zotdb.GroupToRead
	if len(words) > 0 {
		group = words[0]
	}
	valid := false
	for _, g := range zotdb.QueueGroups {
		valid = valid || g == group
	}
	if !valid {
		return fmt.Errorf("unknown group %q. Use one of: %s", group, strings.Join(zotdb.QueueGroups, ", "))
	}

	items, err := zotdb.LoadItems(p.zotero)
	if err != nil {
		return err
	}
	queue := zotdb.InGroup(items, group)
	shown := limited(queue, *limit)
	if err := rememberResults(p, shown); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(shown)
	}
	counts := zotdb.CountGroups(items)
	var summary []string
	for _, g := range zotdb.QueueGroups {
		summary = append(summary, fmt.Sprintf("%s %d", g, counts[g]))
	}
	fmt.Println(strings.Join(summary, "  ·  "))
	printList(shown)
	if len(shown) < len(queue) {
		fmt.Printf("Showing %d of %d\n", len(shown), len(queue))
	}
	return nil
}

func runShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	p := addPathFlags(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	words := parseArgs(fs, args)
	if len(words) != 1 {
		return errors.New("usage: zotty show REF")
	}
	items, err := zotdb.LoadItems(p.zotero)
	if err != nil {
		return err
	}
	it, err := resolveRef(p, items, words[0])
	if err != nil {
		return err
	}
	details, err := zotdb.LoadDetails(p.zotero, it.ID)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(struct {
			zotdb.Item
			zotdb.Details
		}{it, details})
	}

	fmt.Println(it.Title)
	fmt.Println(strings.Join(it.Creators, ", "))
	for _, field := range [][2]string{
		{"Published", strings.TrimSpace(it.Venue + " " + it.Reference)},
		{"Year", it.Year}, {"DOI", it.DOI}, {"URL", it.URL}, {"Status", it.Status},
		{"Tags", strings.Join(it.Tags, ", ")},
		{"Collections", strings.Join(it.Collections, "; ")},
		{"PDF", it.PDF}, {"Key", it.Key},
	} {
		if field[1] != "" {
			fmt.Printf("%-12s %s\n", field[0], field[1])
		}
	}
	if it.Abstract != "" {
		fmt.Printf("\n%s\n", it.Abstract)
	}
	for _, note := range details.Notes {
		fmt.Printf("\nNote\n%s\n", note)
	}
	for _, a := range details.Annotations {
		fmt.Printf("\nHighlight, page %s\n", a.Page)
		if a.Text != "" {
			fmt.Println(a.Text)
		}
		if a.Comment != "" {
			fmt.Println("Comment:", a.Comment)
		}
	}
	return nil
}

func runOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	p := addPathFlags(fs)
	words := parseArgs(fs, args)
	if len(words) != 1 {
		return errors.New("usage: zotty open REF")
	}
	items, err := zotdb.LoadItems(p.zotero)
	if err != nil {
		return err
	}
	it, err := resolveRef(p, items, words[0])
	if err != nil {
		return err
	}
	if it.PDF == "" {
		return fmt.Errorf("%q has no PDF", it.Title)
	}
	if _, err := os.Stat(it.PDF); err != nil {
		return fmt.Errorf("the PDF for %q is missing: %w", it.Title, err)
	}
	return exec.Command("open", it.PDF).Run()
}

func runGrep(args []string) error {
	fs := flag.NewFlagSet("grep", flag.ExitOnError)
	p := addPathFlags(fs)
	limit := fs.Int("limit", defaultLimit, "maximum results, 0 for all")
	asJSON := fs.Bool("json", false, "print JSON")
	words := parseArgs(fs, args)
	if len(words) == 0 {
		return errors.New("usage: zotty grep WORDS")
	}
	ids, err := zotdb.SearchFullText(p.zotero, words)
	if err != nil {
		return err
	}
	items, err := zotdb.LoadItems(p.zotero)
	if err != nil {
		return err
	}
	matched := map[int]bool{}
	for _, id := range ids {
		matched[id] = true
	}
	matches := []zotdb.Item{}
	for _, it := range items {
		if matched[it.ID] {
			matches = append(matches, it)
		}
	}
	shown := limited(matches, *limit)
	if err := rememberResults(p, shown); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(shown)
	}
	if len(matches) == 0 {
		fmt.Println("No PDFs contain that")
		return nil
	}
	printList(shown)
	if len(shown) < len(matches) {
		fmt.Printf("Showing %d of %d papers\n", len(shown), len(matches))
	}
	return nil
}

func runTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	p := addPathFlags(fs)
	fs.Parse(args)
	store, err := inbox.Open(filepath.Join(p.data, "inbox"))
	if err != nil {
		return err
	}
	return tui.Run(tui.Config{ZoteroDir: p.zotero, DataDir: p.data, Store: store, Port: p.port})
}
