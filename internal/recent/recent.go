// Package recent keeps the projects opened last, the most recent first.
package recent

import (
	"path/filepath"
	"slices"
	"strings"
)

// Max is how many projects the list keeps, as IntelliJ's.
const Max = 25

// Add puts path first, once.
func Add(list []string, path string) []string {
	list = Remove(list, path)
	list = append([]string{path}, list...)
	return list[:min(len(list), Max)]
}

func Remove(list []string, path string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(p string) bool { return p == path })
}

// Match keeps the projects whose name, or else whose path, holds query's
// letters in order, ignoring case: "mfac" finds megafactory. Matches on
// the name come first; each keeps its order of recency.
func Match(list []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return list
	}
	var byName, byPath []string
	for _, p := range list {
		switch {
		case subsequence(strings.ToLower(filepath.Base(p)), query):
			byName = append(byName, p)
		case subsequence(strings.ToLower(p), query):
			byPath = append(byPath, p)
		}
	}
	return append(byName, byPath...)
}

func subsequence(s, sub string) bool {
	for _, r := range sub {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}
