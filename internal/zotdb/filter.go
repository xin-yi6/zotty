package zotdb

import (
	"sort"
	"strings"
)

// Queue groups are the reading states shown in the queue, built from the labels
// the Reading List plugin writes.
const (
	GroupToRead     = "to-read"
	GroupInProgress = "in-progress"
	GroupNew        = "new"
	GroupRead       = "read"
)

// QueueGroups lists the groups in display order.
var QueueGroups = []string{GroupToRead, GroupInProgress, GroupNew, GroupRead}

// GroupOf returns the queue group for a status, or "" when it isn't queued.
func GroupOf(status string) string {
	switch {
	case status == "To Read":
		return GroupToRead
	case status == "In Progress":
		return GroupInProgress
	case status == "New":
		return GroupNew
	case strings.HasPrefix(status, "Read"), status == "Printed and read":
		return GroupRead
	}
	return ""
}

// InGroup returns the items in a queue group, keeping their order.
func InGroup(items []Item, group string) []Item {
	matched := []Item{}
	for _, it := range items {
		if GroupOf(it.Status) == group {
			matched = append(matched, it)
		}
	}
	return matched
}

// CountGroups counts items per queue group.
func CountGroups(items []Item) map[string]int {
	counts := map[string]int{}
	for _, it := range items {
		if group := GroupOf(it.Status); group != "" {
			counts[group]++
		}
	}
	return counts
}

// Filter returns the items matching query, best matches first. Every word
// must match. Plain words are recognized by what they match: an author's name,
// a year, or a word of the title, venue, tags, or collections, so "jeanluc 2025"
// needs no special syntax. A word can still name its field: a:author t:title
// y:year s:status c:collection #tag.
func Filter(items []Item, query string) []Item {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return items
	}
	type scored struct {
		item  Item
		score int
	}
	var matches []scored
	for _, it := range items {
		if score, ok := match(it, terms); ok {
			matches = append(matches, scored{it, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	result := make([]Item, len(matches))
	for i, m := range matches {
		result[i] = m.item
	}
	return result
}

// How strongly a plain word matches each field. A name or year says more about
// what the user wants than a word that happens to be in a title.
const (
	scoreAuthor      = 4
	scoreYear        = 4
	scoreTitleWord   = 3
	scoreTitleInside = 1
	scoreOther       = 1
)

func match(it Item, terms []string) (int, bool) {
	score := 0
	for _, term := range terms {
		if field, value, ok := fieldTerm(term); ok {
			if field == nil {
				if !authorMatches(it, value) {
					return 0, false
				}
				continue
			}
			if !strings.Contains(field(it), value) {
				return 0, false
			}
			continue
		}
		best := plainScore(it, term)
		if best == 0 {
			return 0, false
		}
		score += best
	}
	return score, true
}

func plainScore(it Item, term string) int {
	best := 0
	if authorMatches(it, term) {
		best = scoreAuthor
	}
	if isYear(term) && it.Year == term {
		best = max(best, scoreYear)
	}
	title := strings.ToLower(it.Title)
	switch {
	case hasWordPrefix(title, term):
		best = max(best, scoreTitleWord)
	case !isASCII(term) && strings.Contains(title, term):
		// Chinese and Japanese titles have no spaces between words.
		best = max(best, scoreTitleWord)
	case strings.Contains(title, term) && len(term) >= 4:
		best = max(best, scoreTitleInside)
	}
	if best == 0 {
		others := append([]string{it.Venue}, it.Tags...)
		others = append(others, it.Collections...)
		for _, field := range others {
			if hasWordPrefix(strings.ToLower(field), term) {
				return scoreOther
			}
		}
	}
	return best
}

func isYear(term string) bool {
	if len(term) != 4 {
		return false
	}
	for _, r := range term {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// hasWordPrefix reports whether a word of text starts with term, so "picard"
// finds "Picard" but not "Lepicard", and "hologra" finds "holographic".
func hasWordPrefix(text, term string) bool {
	for i := 0; i <= len(text)-len(term); i++ {
		if !strings.HasPrefix(text[i:], term) {
			continue
		}
		if i == 0 || !isWordRune(rune(text[i-1])) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x80
}

func fieldTerm(term string) (func(Item) string, string, bool) {
	if value, ok := strings.CutPrefix(term, "#"); ok && value != "" {
		return func(it Item) string { return lowerJoin(it.Tags) }, value, true
	}
	name, value, ok := strings.Cut(term, ":")
	if !ok || value == "" {
		return nil, "", false
	}
	switch name {
	case "a":
		return nil, value, true
	case "t":
		return func(it Item) string { return strings.ToLower(it.Title) }, value, true
	case "y":
		return func(it Item) string { return it.Year }, value, true
	case "s":
		return func(it Item) string { return strings.ToLower(it.Status) }, value, true
	case "c":
		return func(it Item) string { return lowerJoin(it.Collections) }, value, true
	}
	return nil, "", false
}

// authorMatches reports whether a name, or one part of it, starts with value.
// Hyphens and spaces are ignored, so "jean-luc", "jeanluc", and "jean" all find
// Jean-Luc Picard, while "picard" doesn't find Lepicard.
func authorMatches(it Item, value string) bool {
	want := compactName(value)
	if want == "" {
		return false
	}
	for _, name := range it.Creators {
		for _, part := range nameParts(name) {
			if strings.HasPrefix(part, want) {
				return true
			}
		}
	}
	return false
}

// nameParts returns each word of a name, each hyphenated piece, and the
// given names joined, all lowercase without punctuation.
func nameParts(name string) []string {
	words := strings.Fields(strings.ToLower(name))
	parts := []string{compactName(name)}
	for i, word := range words {
		parts = append(parts, compactName(word))
		if i < len(words)-1 {
			parts = append(parts, compactName(strings.Join(words[:i+1], "")))
		}
		parts = append(parts, strings.FieldsFunc(word, isNameSeparator)...)
	}
	return parts
}

func compactName(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), isNameSeparator), "")
}

func isNameSeparator(r rune) bool {
	return r == '-' || r == '.' || r == ' ' || r == '\''
}

func lowerJoin(values []string) string {
	return strings.ToLower(strings.Join(values, "\n"))
}
