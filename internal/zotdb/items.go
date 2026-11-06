package zotdb

import (
	"database/sql"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Item is a top-level library item.
type Item struct {
	ID           int      `json:"-"`
	Key          string   `json:"key"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	Creators     []string `json:"creators"`
	Surnames     []string `json:"-"`
	Year         string   `json:"year,omitempty"`
	Venue        string   `json:"venue,omitempty"`
	Reference    string   `json:"reference,omitempty"`
	DOI          string   `json:"doi,omitempty"`
	URL          string   `json:"url,omitempty"`
	Abstract     string   `json:"abstract,omitempty"`
	DateAdded    string   `json:"dateAdded"`
	DateModified string   `json:"dateModified"`
	Status       string   `json:"status,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Collections  []string `json:"collections,omitempty"`
	// CollectionIDs are the collections that hold the item directly.
	CollectionIDs []int  `json:"-"`
	PDF           string `json:"pdf,omitempty"`
	Notes         int    `json:"noteCount"`
	Highlights    int    `json:"highlightCount"`
}

// Annotation is a highlight or comment made in a PDF.
type Annotation struct {
	Page    string `json:"page,omitempty"`
	Text    string `json:"text,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Details is the content kept outside Item because it's only needed for one
// paper at a time.
type Details struct {
	Notes       []string     `json:"notes,omitempty"`
	Annotations []Annotation `json:"annotations,omitempty"`
}

// FirstAuthor returns the first creator's surname.
func (i Item) FirstAuthor() string {
	if len(i.Surnames) == 0 {
		return ""
	}
	return i.Surnames[0]
}

var (
	statusPattern = regexp.MustCompile(`(?m)^Read_Status: *(.+?) *$`)
	tagPattern    = regexp.MustCompile(`<[^>]*>`)
)

// Zotero uses different field names for the same idea on different item types.
var (
	titleFields = []string{"title", "caseName", "nameOfAct", "subject"}
	venueFields = []string{"publicationTitle", "proceedingsTitle", "bookTitle", "websiteTitle", "university", "repository"}
)

const excludedTypes = `('attachment', 'note', 'annotation')`

// LoadItems reads every top-level item in the personal library, newest first.
func LoadItems(dataDir string) ([]Item, error) {
	db, err := openLibrary(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var libraryID int
	if err := db.QueryRow(`SELECT libraryID FROM libraries WHERE type = 'user'`).Scan(&libraryID); err != nil {
		return nil, fmt.Errorf("find personal library: %w", err)
	}

	rows, err := db.Query(`
		SELECT i.itemID, i.key, t.typeName, i.dateAdded, i.dateModified
		FROM items i JOIN itemTypes t USING (itemTypeID)
		WHERE i.libraryID = ?1 AND t.typeName NOT IN `+excludedTypes+`
		  AND i.itemID NOT IN (SELECT itemID FROM deletedItems)
		ORDER BY i.dateAdded DESC, i.itemID DESC`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("read items: %w", err)
	}
	defer rows.Close()

	items := []Item{}
	index := map[int]*Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Key, &it.Type, &it.DateAdded, &it.DateModified); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		index[items[i].ID] = &items[i]
	}

	loaders := []func(*snapshot, map[int]*Item, string) error{
		loadFields, loadCreators, loadTags, loadCollections, loadAttachments, loadNotes,
	}
	for _, load := range loaders {
		if err := load(db, index, dataDir); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func loadFields(db *snapshot, index map[int]*Item, _ string) error {
	rows, err := db.Query(`
		SELECT d.itemID, f.fieldName, v.value
		FROM itemData d
		JOIN fieldsCombined f USING (fieldID)
		JOIN itemDataValues v USING (valueID)`)
	if err != nil {
		return fmt.Errorf("read fields: %w", err)
	}
	defer rows.Close()

	fields := map[int]map[string]string{}
	for rows.Next() {
		var id int
		var name, value string
		if err := rows.Scan(&id, &name, &value); err != nil {
			return err
		}
		if index[id] == nil {
			continue
		}
		if fields[id] == nil {
			fields[id] = map[string]string{}
		}
		fields[id][name] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for id, f := range fields {
		it := index[id]
		it.Title = firstField(f, titleFields)
		it.Venue = firstField(f, venueFields)
		it.Reference = reference(f)
		it.DOI = f["DOI"]
		it.URL = f["url"]
		it.Abstract = f["abstractNote"]
		if len(f["date"]) >= 4 {
			it.Year = f["date"][:4]
		}
		if m := statusPattern.FindStringSubmatch(f["extra"]); m != nil {
			it.Status = m[1]
		}
	}
	return nil
}

func firstField(f map[string]string, names []string) string {
	for _, name := range names {
		if v := f[name]; v != "" {
			return v
		}
	}
	return ""
}

// reference builds the short citation shown under a title, such as
// "Journal of High Energy Physics 2026, 30".
func reference(f map[string]string) string {
	var parts []string
	if v := f["volume"]; v != "" {
		parts = append(parts, v)
	}
	if v := f["pages"]; v != "" {
		parts = append(parts, v)
	}
	return strings.Join(parts, ", ")
}

func loadCreators(db *snapshot, index map[int]*Item, _ string) error {
	rows, err := db.Query(`
		SELECT ic.itemID, c.firstName, c.lastName
		FROM itemCreators ic JOIN creators c USING (creatorID)
		ORDER BY ic.itemID, ic.orderIndex`)
	if err != nil {
		return fmt.Errorf("read creators: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var first, last string
		if err := rows.Scan(&id, &first, &last); err != nil {
			return err
		}
		if it := index[id]; it != nil {
			it.Creators = append(it.Creators, strings.TrimSpace(first+" "+last))
			it.Surnames = append(it.Surnames, last)
		}
	}
	return rows.Err()
}

func loadTags(db *snapshot, index map[int]*Item, _ string) error {
	rows, err := db.Query(`SELECT it.itemID, t.name FROM itemTags it JOIN tags t USING (tagID) ORDER BY t.name`)
	if err != nil {
		return fmt.Errorf("read tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		if it := index[id]; it != nil {
			it.Tags = append(it.Tags, name)
		}
	}
	return rows.Err()
}

func loadCollections(db *snapshot, index map[int]*Item, _ string) error {
	rows, err := db.Query(`
		WITH RECURSIVE tree(id, path) AS (
			SELECT collectionID, collectionName FROM collections WHERE parentCollectionID IS NULL
			UNION ALL
			SELECT c.collectionID, t.path || ' / ' || c.collectionName
			FROM collections c JOIN tree t ON c.parentCollectionID = t.id
		)
		SELECT ci.itemID, ci.collectionID, t.path
		FROM collectionItems ci JOIN tree t ON t.id = ci.collectionID
		WHERE ci.collectionID NOT IN (SELECT collectionID FROM deletedCollections)
		ORDER BY t.path`)
	if err != nil {
		return fmt.Errorf("read collections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, collection int
		var path string
		if err := rows.Scan(&id, &collection, &path); err != nil {
			return err
		}
		if it := index[id]; it != nil {
			it.Collections = append(it.Collections, path)
			it.CollectionIDs = append(it.CollectionIDs, collection)
		}
	}
	return rows.Err()
}

func loadAttachments(db *snapshot, index map[int]*Item, dataDir string) error {
	rows, err := db.Query(`
		SELECT a.parentItemID, i.key, a.path,
		       (SELECT count(*) FROM itemAnnotations n WHERE n.parentItemID = a.itemID)
		FROM itemAttachments a JOIN items i USING (itemID)
		WHERE a.contentType = 'application/pdf' AND a.parentItemID IS NOT NULL AND a.path IS NOT NULL
		  AND i.itemID NOT IN (SELECT itemID FROM deletedItems)
		ORDER BY a.itemID`)
	if err != nil {
		return fmt.Errorf("read attachments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var parent, highlights int
		var key, path string
		if err := rows.Scan(&parent, &key, &path, &highlights); err != nil {
			return err
		}
		it := index[parent]
		if it == nil {
			continue
		}
		it.Highlights += highlights
		if it.PDF == "" {
			it.PDF = attachmentPath(dataDir, key, path)
		}
	}
	return rows.Err()
}

// attachmentPath resolves Zotero's stored path: "storage:" files live in a
// folder named after the attachment's key, and other paths are absolute links.
func attachmentPath(dataDir, key, path string) string {
	if name, ok := strings.CutPrefix(path, "storage:"); ok {
		return filepath.Join(dataDir, "storage", key, name)
	}
	if strings.HasPrefix(path, "attachments:") {
		return ""
	}
	return path
}

func loadNotes(db *snapshot, index map[int]*Item, _ string) error {
	rows, err := db.Query(`
		SELECT parentItemID, count(*) FROM itemNotes
		WHERE parentItemID IS NOT NULL AND itemID NOT IN (SELECT itemID FROM deletedItems)
		GROUP BY parentItemID`)
	if err != nil {
		return fmt.Errorf("read notes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, count int
		if err := rows.Scan(&id, &count); err != nil {
			return err
		}
		if it := index[id]; it != nil {
			it.Notes = count
		}
	}
	return rows.Err()
}

// LoadDetails reads the notes and PDF annotations of one item.
func LoadDetails(dataDir string, id int) (Details, error) {
	db, err := openLibrary(dataDir)
	if err != nil {
		return Details{}, err
	}
	defer db.Close()

	var d Details
	rows, err := db.Query(`
		SELECT note FROM itemNotes
		WHERE parentItemID = ?1 AND itemID NOT IN (SELECT itemID FROM deletedItems)
		ORDER BY itemID`, id)
	if err != nil {
		return d, fmt.Errorf("read notes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var note sql.NullString
		if err := rows.Scan(&note); err != nil {
			return d, err
		}
		if text := plainText(note.String); text != "" {
			d.Notes = append(d.Notes, text)
		}
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	annotations, err := db.Query(`
		SELECT n.text, n.comment, n.pageLabel
		FROM itemAnnotations n JOIN itemAttachments a ON n.parentItemID = a.itemID
		WHERE a.parentItemID = ?1 AND n.itemID NOT IN (SELECT itemID FROM deletedItems)
		ORDER BY n.sortIndex`, id)
	if err != nil {
		return d, fmt.Errorf("read annotations: %w", err)
	}
	defer annotations.Close()
	for annotations.Next() {
		var text, comment, page sql.NullString
		if err := annotations.Scan(&text, &comment, &page); err != nil {
			return d, err
		}
		d.Annotations = append(d.Annotations, Annotation{Page: page.String, Text: text.String, Comment: comment.String})
	}
	return d, annotations.Err()
}

// plainText turns a note's HTML into readable text.
func plainText(note string) string {
	for _, block := range []string{"</p>", "</div>", "<br>", "<br/>", "</li>", "</h1>", "</h2>"} {
		note = strings.ReplaceAll(note, block, "\n")
	}
	text := html.UnescapeString(tagPattern.ReplaceAllString(note, ""))
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// SearchFullText returns the items whose indexed text contains every term,
// using Zotero's fulltext.sqlite. The index stores no text, so it can say which
// PDFs match but not where.
func SearchFullText(dataDir string, terms []string) ([]int, error) {
	path := filepath.Join(dataDir, "fulltext.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("this Zotero has no full-text index: %w", err)
	}
	ft, err := sql.Open("sqlite", fileDSN(path, "mode=ro"))
	if err != nil {
		return nil, err
	}
	defer ft.Close()

	match := matchExpression(terms)
	table := "fulltextContent"
	if !isASCII(match) {
		// Zotero indexes CJK text separately, in a trigram table.
		table = "fulltextContentCJK"
	}
	rows, err := ft.Query(`SELECT rowid FROM `+table+` WHERE `+table+` MATCH ?1`, match)
	if err != nil {
		return nil, fmt.Errorf("search full text: %w", err)
	}
	defer rows.Close()

	attachments := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		attachments = append(attachments, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return parentsOf(dataDir, attachments)
}

func matchExpression(terms []string) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return false
		}
	}
	return true
}

// parentsOf maps attachment IDs to their parent items.
func parentsOf(dataDir string, attachments []int) ([]int, error) {
	db, err := openLibrary(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT itemID, parentItemID FROM itemAttachments WHERE parentItemID IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("read attachments: %w", err)
	}
	defer rows.Close()
	parents := map[int]int{}
	for rows.Next() {
		var id, parent int
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		parents[id] = parent
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := map[int]bool{}
	result := []int{}
	for _, id := range attachments {
		if parent, ok := parents[id]; ok && !seen[parent] {
			seen[parent] = true
			result = append(result, parent)
		}
	}
	sort.Ints(result)
	return result, nil
}
