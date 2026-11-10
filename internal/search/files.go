package search

import (
	"bytes"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// pruned are folders no one opens a file from, and that are big enough to
// make a walk slow; used only where git can't say what the project holds.
var pruned = map[string]bool{
	".git": true, "node_modules": true, "target": true, "build": true, "out": true,
	".gradle": true, ".idea": true, ".m2": true, ".cache": true, ".venv": true,
}

// maxFiles bounds the list so a huge tree can't stall the dialog.
const maxFiles = 200_000

// Files lists the files of the project at root, relative with slashes:
// what git tracks plus what it doesn't ignore, so .gitignore is honoured as
// in the tree. Outside a repository it walks the folder instead.
func Files(root string) []string {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if out, err := cmd.Output(); err == nil {
		var files []string
		for p := range bytes.SplitSeq(out, []byte{0}) {
			if len(p) > 0 {
				files = append(files, string(p))
			}
		}
		// A tracked file deleted on disk still shows in ls-files.
		return slices.Compact(files)
	}
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir():
			if p != root && pruned[d.Name()] {
				return filepath.SkipDir
			}
		case len(files) >= maxFiles:
			return filepath.SkipAll
		default:
			if rel, err := filepath.Rel(root, p); err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return files
}

// Hit is a file that matched, with the runes of its name to highlight.
type Hit struct {
	Path    string
	Name    string
	Indices []int // into Name; empty when the path matched, not the name
	score   int
}

// MaxHits is how many hits Find returns, as dbx's Quick Open.
const MaxHits = 200

// Find ranks the files by how their name fits query, then by how their
// path does, which ranks lower; ties go to the shorter name, then the
// name's order. An empty query keeps files as given.
func Find(files []string, query string) []Hit {
	var hits []Hit
	for _, f := range files {
		name := f[strings.LastIndexByte(f, '/')+1:]
		if strings.TrimSpace(query) == "" {
			hits = append(hits, Hit{Path: f, Name: name})
			continue
		}
		if m, ok := Text(query, name); ok {
			hits = append(hits, Hit{f, name, m.Indices, m.Score})
		} else if m, ok := Text(query, f); ok {
			hits = append(hits, Hit{f, name, nil, m.Score + 1000})
		}
	}
	if strings.TrimSpace(query) != "" {
		slices.SortFunc(hits, func(a, b Hit) int {
			if a.score != b.score {
				return a.score - b.score
			}
			if len(a.Name) != len(b.Name) {
				return len(a.Name) - len(b.Name)
			}
			return strings.Compare(a.Path, b.Path)
		})
	}
	return hits[:min(len(hits), MaxHits)]
}
