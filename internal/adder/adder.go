// Package adder adds a paper by arXiv ID or DOI. It goes through the inbox, the
// same path as a save from Chrome, so it works whether Zotero is open or not.
package adder

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xin-yi6/zotty/internal/fetch"
	"github.com/xin-yi6/zotty/internal/importer"
	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

// Options say where to add the paper and how to file it.
type Options struct {
	ZoteroDir string
	Port      int
	Store     *inbox.Store
	// Target is a save target such as "C12", or empty for the whole library.
	Target string
	Tags   []string
	// Force adds the paper even if it's already in the library.
	Force bool
}

// Result describes what happened.
type Result struct {
	Title string
	// PDFError says why there's no PDF, or is nil when there is one.
	PDFError error
	// Imported is true when the paper went straight into a running Zotero;
	// otherwise it waits in the inbox.
	Imported bool
}

// ErrDuplicate means the paper is already in the library.
type ErrDuplicate struct{ Title, Key string }

func (e ErrDuplicate) Error() string {
	return fmt.Sprintf("already in your library: %s (%s)", e.Title, e.Key)
}

// ErrNoFreePDF is the reason a DOI comes without a PDF.
var ErrNoFreePDF = errors.New("no free PDF for a DOI. Save it from the publisher's page in Chrome to get one")

// Add looks the paper up, saves it to the inbox, and imports it at once if
// Zotero is running.
func Add(id string, o Options) (Result, error) {
	items, err := zotdb.LoadItems(o.ZoteroDir)
	if err != nil {
		return Result{}, err
	}
	fetcher := fetch.NewClient()
	paper, err := fetcher.Lookup(id)
	if err != nil {
		return Result{}, err
	}
	if existing, ok := FindExisting(items, paper); ok && !o.Force {
		return Result{}, ErrDuplicate{existing.Title, existing.Key}
	}
	result := Result{Title: paper.Title}

	itemID, err := randomID()
	if err != nil {
		return result, err
	}
	paper.Item["id"] = itemID
	paper.Item["attachments"] = []any{}
	raw, err := json.Marshal(paper.Item)
	if err != nil {
		return result, err
	}
	itemType, _ := paper.Item["itemType"].(string)
	url, _ := paper.Item["url"].(string)
	entry, err := o.Store.Create(inbox.Entry{
		Kind: "saveItems", ConnectorKey: itemID, Title: paper.Title, ItemType: itemType, URL: url,
		Target: o.Target, Tags: o.Tags, Item: raw,
	})
	if err != nil {
		return result, err
	}

	result.PDFError = ErrNoFreePDF
	if paper.PDFURL != "" {
		pdf, err := fetcher.DownloadPDF(paper.PDFURL)
		result.PDFError = err
		if err == nil {
			_, err = o.Store.AddFile(entry.ID, inbox.Attachment{
				ID: itemID + "pdf", Title: "Preprint PDF", URL: paper.PDFURL, ContentType: "application/pdf",
			}, bytes.NewReader(pdf))
			if err != nil {
				return result, err
			}
		}
	}

	client := importer.NewClient(o.Port)
	if client.Ping() != nil {
		return result, nil
	}
	run, err := importer.Run(client, o.Store, zotdb.UserLibraryTarget(o.ZoteroDir), func(string, ...any) {})
	if err != nil {
		return result, err
	}
	if run.Failed > 0 {
		return result, errors.New("Zotero didn't accept the paper. It's still in the inbox and will be tried again")
	}
	result.Imported = true
	return result, nil
}

// FindExisting matches on DOI or arXiv ID, which survive edits to titles.
func FindExisting(items []zotdb.Item, paper fetch.Paper) (zotdb.Item, bool) {
	doi := strings.ToLower(paper.DOI)
	for _, it := range items {
		if doi != "" && strings.ToLower(it.DOI) == doi {
			return it, true
		}
		if paper.ArXiv != "" && (strings.Contains(it.URL, "arxiv.org/abs/"+paper.ArXiv) ||
			strings.EqualFold(it.DOI, "10.48550/arXiv."+paper.ArXiv)) {
			return it, true
		}
	}
	return zotdb.Item{}, false
}

func randomID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
