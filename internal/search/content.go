package search

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// Query is what Find in Files looks for.
type Query struct {
	Text                        string
	MatchCase, WholeWord, Regex bool
}

// Limits keep one search from reading the whole disk or filling the dialog.
const (
	MaxMatches   = 500
	maxPerFile   = 100
	maxFileBytes = 1 << 20
	// snippetBytes bounds the line a row shows, cut around the match.
	snippetBytes = 200
	snippetLead  = 40
)

// Found is one place the text was found.
type Found struct {
	Path     string
	Line     int // from 0
	Col, Len int // bytes into the line, and of the match
	// Text is the line as a row shows it, its indent trimmed and a long
	// line cut around the match; From and To are the match's bytes in it.
	Text     string
	From, To int
}

// Result is what a search found, in the order of the files given.
type Result struct {
	Matches []Found
	// Files is how many files hold a match.
	Files int
	// More reports that there were more matches than MaxMatches.
	More bool
}

func (q Query) compile() (*regexp.Regexp, error) {
	expr := q.Text
	if !q.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if q.WholeWord {
		if q.Regex {
			expr = `\b(?:` + expr + `)\b`
		} else {
			// \b beside a symbol would demand a word next to it, which
			// the user did not type.
			first, _ := utf8.DecodeRuneInString(q.Text)
			last, _ := utf8.DecodeLastRuneInString(q.Text)
			if isWord(first) {
				expr = `\b` + expr
			}
			if isWord(last) {
				expr += `\b`
			}
		}
	}
	flags := "(?m)"
	if !q.MatchCase {
		flags = "(?mi)"
	}
	return regexp.Compile(flags + expr)
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// Content searches the files, relative to root, for the query, off as many
// threads as there are cores. It stops early when ctx is done, which is
// not an error: the result is of the files read by then. A query that is
// not a regular expression is the only error.
func Content(ctx context.Context, root string, files []string, q Query) (Result, error) {
	var res Result
	if q.Text == "" {
		return res, nil
	}
	re, err := q.compile()
	if err != nil {
		return res, err
	}
	perFile := make([][]Found, len(files))
	var total atomic.Int64
	var next atomic.Int64
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil && total.Load() < MaxMatches {
				i := int(next.Add(1) - 1)
				if i >= len(files) {
					return
				}
				// A file that started is read to its end, so that the
				// files before the cap are all in the result.
				perFile[i] = searchFile(re, root, files[i])
				total.Add(int64(len(perFile[i])))
			}
		}()
	}
	wg.Wait()
	for _, ms := range perFile {
		if len(ms) == 0 {
			continue
		}
		res.Files++
		for _, m := range ms {
			if len(res.Matches) == MaxMatches {
				res.More = true
				return res, nil
			}
			res.Matches = append(res.Matches, m)
		}
	}
	return res, nil
}

func searchFile(re *regexp.Regexp, root, rel string) []Found {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil
	}
	var out []Found
	line, counted := 0, 0
	for _, loc := range re.FindAllIndex(data, maxPerFile*4) {
		start, end := loc[0], loc[1]
		line += bytes.Count(data[counted:start], []byte{'\n'})
		counted = start
		from := bytes.LastIndexByte(data[:start], '\n') + 1
		to := len(data)
		if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
			to = start + i
		}
		// A pattern may run past the line; the row shows only its part.
		end = min(end, to)
		if end <= start {
			continue
		}
		out = append(out, snippet(rel, line, data[from:to], start-from, end-start))
		if len(out) == maxPerFile {
			break
		}
	}
	return out
}

// snippet is the match as a row shows it.
func snippet(rel string, line int, text []byte, col, n int) Found {
	text = bytes.TrimRight(text, "\r")
	lead := 0
	for lead < col && (text[lead] == ' ' || text[lead] == '\t') {
		lead++
	}
	from := lead
	if col-from > snippetLead && len(text)-from > snippetBytes {
		from = col - snippetLead
	}
	for from < col && !utf8.RuneStart(text[from]) {
		from++
	}
	to := min(len(text), from+max(snippetBytes, col-from+n))
	for to > from && to < len(text) && !utf8.RuneStart(text[to]) {
		to--
	}
	s := strings.ToValidUTF8(string(text[from:to]), "�")
	return Found{Path: rel, Line: line, Col: col, Len: n, Text: s, From: col - from, To: min(col-from+n, len(s))}
}
