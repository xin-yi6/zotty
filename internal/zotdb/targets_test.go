package zotdb

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTargets(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "zotero.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE libraries (libraryID INTEGER PRIMARY KEY, type TEXT);
		CREATE TABLE collections (collectionID INTEGER PRIMARY KEY, collectionName TEXT, parentCollectionID INT, libraryID INT);
		CREATE TABLE deletedCollections (collectionID INT);
		CREATE TABLE tags (tagID INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE itemTags (itemID INT, tagID INT);
		CREATE TABLE items (itemID INTEGER PRIMARY KEY, libraryID INT);
		INSERT INTO libraries VALUES (1, 'user'), (2, 'group');
		INSERT INTO collections VALUES
			(1, 'QCD theory', NULL, 1),
			(2, 'Phase structure', 1, 1),
			(3, 'Axial anomaly', 1, 1),
			(4, 'Deleted', NULL, 1),
			(5, 'Group collection', NULL, 2),
			(6, 'AI', NULL, 1);
		INSERT INTO deletedCollections VALUES (4);
		INSERT INTO items VALUES (1, 1), (2, 2);
		INSERT INTO tags VALUES (1, 'QCD'), (2, 'Group tag');
		INSERT INTO itemTags VALUES (1, 1), (2, 2);
	`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	targets, tags, err := LoadTargets(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{ID: "L1", Name: "My Library", FilesEditable: true},
		{ID: "C6", Name: "AI", FilesEditable: true, Level: 1},
		{ID: "C1", Name: "QCD theory", FilesEditable: true, Level: 1},
		{ID: "C3", Name: "Axial anomaly", FilesEditable: true, Level: 2},
		{ID: "C2", Name: "Phase structure", FilesEditable: true, Level: 2},
	}
	if len(targets) != len(want) {
		t.Fatalf("targets = %+v", targets)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("target %d = %+v, want %+v", i, targets[i], want[i])
		}
	}
	if len(tags) != 1 || tags[0].Tag != "QCD" {
		t.Errorf("tags = %+v", tags)
	}
}

func TestLoadCollectionsTree(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "zotero.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE libraries (libraryID INTEGER PRIMARY KEY, type TEXT);
		CREATE TABLE collections (collectionID INTEGER PRIMARY KEY, collectionName TEXT, parentCollectionID INT, libraryID INT, key TEXT);
		CREATE TABLE deletedCollections (collectionID INT);
		CREATE TABLE collectionItems (collectionID INT, itemID INT);
		CREATE TABLE items (itemID INTEGER PRIMARY KEY, itemTypeID INT, libraryID INT);
		CREATE TABLE itemTypes (itemTypeID INTEGER PRIMARY KEY, typeName TEXT);
		CREATE TABLE deletedItems (itemID INT);
		INSERT INTO libraries VALUES (1, 'user');
		INSERT INTO itemTypes VALUES (1, 'journalArticle'), (2, 'attachment');
		INSERT INTO items VALUES (1, 1, 1), (2, 1, 1), (3, 2, 1), (4, 1, 1);
		INSERT INTO deletedItems VALUES (4);
		INSERT INTO collections VALUES
			(1, 'QCD', NULL, 1, 'KQCD'), (2, 'Chiral PT', 1, 1, 'KCPT'), (3, 'Axial', 1, 1, 'KAX'), (4, 'AI', NULL, 1, 'KAI'), (5, 'Gone', 4, 1, 'KG');
		INSERT INTO deletedCollections VALUES (5);
		INSERT INTO collectionItems VALUES (1, 1), (1, 2), (1, 3), (1, 4), (2, 1);
	`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadCollections(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Collection{
		{ID: 4, Key: "KAI", Name: "AI"},
		{ID: 1, Key: "KQCD", Name: "QCD", Items: 2, HasChildren: true},
		{ID: 3, Key: "KAX", ParentID: 1, Name: "Axial", Depth: 1},
		{ID: 2, Key: "KCPT", ParentID: 1, Name: "Chiral PT", Depth: 1, Items: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFindCollection(t *testing.T) {
	collections := []Collection{
		{ID: 1, Name: "QCD theory"},
		{ID: 2, ParentID: 1, Name: "Phase structure"},
		{ID: 3, Name: "Holography"},
		{ID: 4, ParentID: 3, Name: "Phase structure"},
		{ID: 5, ParentID: 3, Name: "Glueball"},
	}
	for query, want := range map[string]int{
		"qcd theory / phase structure": 2, "QCD theory/Phase structure": 2, "glueball": 5, "Holography": 3,
	} {
		got, err := FindCollection(collections, query)
		if err != nil || got.ID != want {
			t.Errorf("%q: got %d, %v", query, got.ID, err)
		}
	}
	if _, err := FindCollection(collections, "phase structure"); err == nil || !strings.Contains(err.Error(), "Holography / Phase structure") {
		t.Errorf("ambiguous name: %v", err)
	}
}
