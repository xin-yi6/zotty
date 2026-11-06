// Package zotdb reads Zotero's local database. It opens the file read-only and
// never writes to it.
package zotdb

import (
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Target is a place the browser popup can offer for a save.
type Target struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	FilesEditable bool   `json:"filesEditable"`
	Level         int    `json:"level"`
}

// Tag is a library tag for the popup's autocomplete.
type Tag struct {
	Tag  string `json:"tag"`
	Type int    `json:"type"`
}

// LibraryName is the name Zotero gives the personal library.
const LibraryName = "My Library"

// LoadTargets returns the personal library followed by its collection tree,
// plus the tags used in that library.
func LoadTargets(dataDir string) ([]Target, []Tag, error) {
	db, err := openLibrary(dataDir)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	var libraryID int
	if err := db.QueryRow(`SELECT libraryID FROM libraries WHERE type = 'user'`).Scan(&libraryID); err != nil {
		return nil, nil, fmt.Errorf("find personal library: %w", err)
	}

	rows, err := db.Query(`
		WITH RECURSIVE tree(id, name, level, sortPath) AS (
			SELECT collectionID, collectionName, 1, lower(collectionName)
			FROM collections
			WHERE libraryID = ?1 AND parentCollectionID IS NULL
			UNION ALL
			SELECT c.collectionID, c.collectionName, t.level + 1,
			       t.sortPath || char(31) || lower(c.collectionName)
			FROM collections c JOIN tree t ON c.parentCollectionID = t.id
		)
		SELECT id, name, level FROM tree
		WHERE id NOT IN (SELECT collectionID FROM deletedCollections)
		ORDER BY sortPath`, libraryID)
	if err != nil {
		return nil, nil, fmt.Errorf("read collections: %w", err)
	}
	defer rows.Close()

	targets := []Target{{ID: fmt.Sprintf("L%d", libraryID), Name: LibraryName, FilesEditable: true}}
	for rows.Next() {
		var id, level int
		var name string
		if err := rows.Scan(&id, &name, &level); err != nil {
			return nil, nil, err
		}
		targets = append(targets, Target{ID: fmt.Sprintf("C%d", id), Name: name, FilesEditable: true, Level: level})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	tagRows, err := db.Query(`
		SELECT DISTINCT t.name FROM tags t
		JOIN itemTags it ON it.tagID = t.tagID
		JOIN items i ON i.itemID = it.itemID
		WHERE i.libraryID = ?1
		ORDER BY t.name`, libraryID)
	if err != nil {
		return nil, nil, fmt.Errorf("read tags: %w", err)
	}
	defer tagRows.Close()

	tags := []Tag{}
	for tagRows.Next() {
		var name string
		if err := tagRows.Scan(&name); err != nil {
			return nil, nil, err
		}
		tags = append(tags, Tag{Tag: name})
	}
	return targets, tags, tagRows.Err()
}

// UserLibraryTarget returns the save target for the personal library.
func UserLibraryTarget(dataDir string) string {
	targets, _, err := LoadTargets(dataDir)
	if err != nil || len(targets) == 0 {
		return "L1"
	}
	return targets[0].ID
}

// Collection is a node of the collection tree.
type Collection struct {
	ID       int
	Key      string
	ParentID int
	Name     string
	Depth    int
	// Items counts the items directly in the collection, as Zotero shows them.
	Items       int
	HasChildren bool
}

// LoadCollections returns the personal library's collections in tree order,
// each parent before its children, siblings sorted by name.
func LoadCollections(dataDir string) ([]Collection, error) {
	db, err := openLibrary(dataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		WITH RECURSIVE tree(id, key, parent, name, depth, sortPath) AS (
			SELECT collectionID, key, 0, collectionName, 0, lower(collectionName)
			FROM collections
			WHERE parentCollectionID IS NULL
			  AND libraryID = (SELECT libraryID FROM libraries WHERE type = 'user')
			UNION ALL
			SELECT c.collectionID, c.key, t.id, c.collectionName, t.depth + 1,
			       t.sortPath || char(31) || lower(c.collectionName)
			FROM collections c JOIN tree t ON c.parentCollectionID = t.id
		)
		SELECT t.id, t.key, t.parent, t.name, t.depth,
		       (SELECT count(*) FROM collectionItems ci
		        JOIN items i ON i.itemID = ci.itemID
		        JOIN itemTypes it ON it.itemTypeID = i.itemTypeID
		        WHERE ci.collectionID = t.id AND it.typeName NOT IN ` + excludedTypes + `
		          AND i.itemID NOT IN (SELECT itemID FROM deletedItems)),
		       EXISTS (SELECT 1 FROM collections c WHERE c.parentCollectionID = t.id
		               AND c.collectionID NOT IN (SELECT collectionID FROM deletedCollections))
		FROM tree t
		WHERE t.id NOT IN (SELECT collectionID FROM deletedCollections)
		ORDER BY t.sortPath`)
	if err != nil {
		return nil, fmt.Errorf("read collections: %w", err)
	}
	defer rows.Close()

	collections := []Collection{}
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Key, &c.ParentID, &c.Name, &c.Depth, &c.Items, &c.HasChildren); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

// CollectionPath names a collection with its parents, as in "QCD / Chiral PT".
func CollectionPath(collections []Collection, id int) string {
	byID := map[int]Collection{}
	for _, c := range collections {
		byID[c.ID] = c
	}
	var parts []string
	for c, ok := byID[id]; ok; c, ok = byID[c.ParentID] {
		parts = append([]string{c.Name}, parts...)
	}
	return strings.Join(parts, " / ")
}

// FindCollection finds a collection by its full path, such as
// "QCD theory / Phase structure", or by a name that only one collection has.
func FindCollection(collections []Collection, name string) (Collection, error) {
	want := strings.ToLower(strings.Join(strings.Fields(name), " "))
	var byName []Collection
	for _, c := range collections {
		path := strings.ToLower(CollectionPath(collections, c.ID))
		if path == want || strings.ReplaceAll(path, " / ", "/") == strings.ReplaceAll(want, " / ", "/") {
			return c, nil
		}
		if strings.ToLower(c.Name) == want {
			byName = append(byName, c)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Collection{}, fmt.Errorf("no collection named %q", name)
	}
	var paths []string
	for _, c := range byName {
		paths = append(paths, CollectionPath(collections, c.ID))
	}
	return Collection{}, fmt.Errorf("%d collections are named %q. Use the full path: %s", len(byName), name, strings.Join(paths, "; "))
}
