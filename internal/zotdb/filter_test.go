package zotdb

import (
	"strings"
	"testing"
)

func TestAuthorSearch(t *testing.T) {
	items := []Item{
		{Key: "me", Title: "Phase structure", Creators: []string{"William Riker", "Jean-Luc Picard"}},
		{Key: "beverly", Title: "Other", Creators: []string{"Beverly Picard"}},
		{Key: "lepicard", Title: "Asymptotic freedom", Creators: []string{"Csaba Csáki", "Anna Lepicard"}},
		{Key: "single", Title: "Field mode name", Creators: []string{"ATLAS Collaboration"}},
	}
	cases := map[string][]string{
		"a:Jean-Luc":       {"me"},
		"a:jeanluc":        {"me"},
		"a:jean":           {"me"},
		"a:picard":         {"me", "beverly"},
		"a:lepi":           {"lepicard"},
		"a:picard a:riker": {"me"},
		"a:csáki":          {"lepicard"},
		"a:atlas":          {"single"},
		"a:jean-luc phase": {"me"},
		"a:luc":            {"me"},
		"a:card":           nil,
		"a:jeanlucpicard":  {"me"},
	}
	for query, want := range cases {
		got := Filter(items, query)
		if len(got) != len(want) {
			t.Errorf("%q: got %d results, want %v", query, len(got), want)
			continue
		}
		for i := range want {
			if got[i].Key != want[i] {
				t.Errorf("%q: result %d = %s, want %s", query, i, got[i].Key, want[i])
			}
		}
	}
}

func TestPlainWordsSearchFullNames(t *testing.T) {
	items := []Item{{Key: "me", Title: "Phase structure", Creators: []string{"Jean-Luc Picard"}}}
	if len(Filter(items, "jean-luc")) != 1 {
		t.Error("a given name without a: should still match")
	}
}

func TestPlainSearchRecognizesFields(t *testing.T) {
	items := []Item{
		{Key: "mine", Title: "Phase structure of QCD", Creators: []string{"Jean-Luc Picard"}, Year: "2025"},
		{Key: "old", Title: "Phase structure revisited", Creators: []string{"Jean-Luc Picard"}, Year: "2022"},
		{Key: "lepi", Title: "Asymptotic freedom", Creators: []string{"Anna Lepicard"}, Year: "2025"},
		{Key: "picardtitle", Title: "On Picard's conjecture", Creators: []string{"A Smith"}, Year: "2020", Tags: []string{"holography"}},
		{Key: "cjk", Title: "全息量子色动力学的相结构", Creators: []string{"王"}, Year: "2026"},
	}
	cases := map[string][]string{
		"jeanluc 2025":      {"mine"},
		"jean-luc":          {"mine", "old"},
		"picard":            {"mine", "old", "picardtitle"},
		"phase 2022":        {"old"},
		"holography":        {"picardtitle"},
		"相结构":               {"cjk"},
		"asymptotic picard": nil,
		"struct":            {"mine", "old"},
	}
	for query, want := range cases {
		got := Filter(items, query)
		var keys []string
		for _, it := range got {
			keys = append(keys, it.Key)
		}
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Errorf("%q = %v, want %v", query, keys, want)
		}
	}
}
