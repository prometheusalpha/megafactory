// Package search finds a project's files by name, ranking them as dbx's
// Quick Open ranks its items.
package search

import (
	"maps"
	"slices"
	"strings"
	"unicode"
)

// Kind is how a query matched a text, best first.
type Kind uint8

const (
	Exact Kind = iota
	Initials
	Prefix
	WordPrefix
	Substring
	Fuzzy
)

// Match is how well a query fits a text, and where. Lower Score is better;
// each Kind owns a band of 100, so a worse kind never beats a better one.
type Match struct {
	Kind  Kind
	Score int
	// Indices are the runes of the text the query matched, in order.
	Indices []int
}

type word struct {
	text  []rune
	start int
}

// words splits an identifier at separators and at a lower-to-upper step,
// so "OrderServiceImpl.java" is Order, Service, Impl, java.
func words(text []rune) []word {
	var ws []word
	start := -1
	push := func(end int) {
		if start >= 0 && end > start {
			ws = append(ws, word{text[start:end], start})
		}
	}
	for i, r := range text {
		if strings.ContainsRune("_-. /\\", r) {
			push(i)
			start = -1
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		if p := text[i-1]; p >= 'a' && p <= 'z' && r >= 'A' && r <= 'Z' {
			push(i)
			start = i
		}
	}
	push(len(text))
	return ws
}

func span(start, n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = start + i
	}
	return s
}

// Text matches query against text, ignoring case. ok is false when the
// query does not fit; an empty query fits everything with no indices.
func Text(query, text string) (m Match, ok bool) {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(q) == 0 {
		return Match{Kind: Exact}, true
	}
	t := []rune(text)
	lt := []rune(strings.ToLower(text))
	// Lowercasing may change a rune's width in odd scripts; indices then
	// would not line up with text, so match nothing rather than highlight wrong.
	if len(lt) != len(t) {
		return Match{}, false
	}
	if slices.Equal(lt, q) {
		return Match{Exact, 1, span(0, len(t))}, true
	}

	ws := words(t)
	if len(ws) >= 2 {
		initials := make([]rune, len(ws))
		idx := make([]int, len(ws))
		for i, w := range ws {
			initials[i] = unicode.ToLower(w.text[0])
			idx[i] = w.start
		}
		if slices.Equal(initials, q) {
			return Match{Initials, 100 + min(len(ws), 99), idx}, true
		}
	}

	if len(lt) >= len(q) && slices.Equal(lt[:len(q)], q) {
		return Match{Prefix, 200 + min(len(t)-len(q), 99), span(0, len(q))}, true
	}

	if idx := wordPrefixes(ws, q); idx != nil {
		return Match{WordPrefix, 300 + min(len(t)-len(q), 99), idx}, true
	}

	if i := indexRunes(lt, q); i >= 0 {
		return Match{Substring, 400 + min(i, 99), span(i, len(q))}, true
	}

	if len(q) < 2 {
		return Match{}, false
	}
	var idx []int
	qi := 0
	for i := 0; i < len(lt) && qi < len(q); i++ {
		if lt[i] == q[qi] {
			idx = append(idx, i)
			qi++
		}
	}
	if qi == len(q) {
		return Match{Fuzzy, 500 + min(idx[len(idx)-1]-idx[0]+1-len(q), 99), idx}, true
	}
	return Match{}, false
}

func indexRunes(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// prefixState is a partial reading of the query as prefixes of words:
// "osi" is Order+Service+Impl's "o", "s", "i".
type prefixState struct {
	qi, first, last, used int
	idx                   []int
}

// cost prefers prefixes of neighbouring words, then fewer words.
func (s prefixState) cost() int {
	if s.used == 0 {
		return 0
	}
	return (s.last-s.first-s.used+1)*10 + s.used
}

// wordPrefixes reads q as prefixes of two or more words, in order, or nil.
func wordPrefixes(ws []word, q []rune) []int {
	type key struct{ qi, used int }
	states := map[key]prefixState{{0, 0}: {first: -1, last: -1}}
	for wi, w := range ws {
		lw := []rune(strings.ToLower(string(w.text)))
		next := maps.Clone(states)
		for _, s := range states {
			for n := 1; n <= min(len(lw), len(q)-s.qi); n++ {
				if lw[n-1] != q[s.qi+n-1] {
					break
				}
				c := prefixState{qi: s.qi + n, first: s.first, last: wi, used: s.used + 1,
					idx: append(slices.Clone(s.idx), span(w.start, n)...)}
				if s.used == 0 {
					c.first = wi
				}
				k := key{c.qi, c.used}
				if cur, ok := next[k]; !ok || c.cost() < cur.cost() {
					next[k] = c
				}
			}
		}
		states = next
	}
	var best *prefixState
	for _, s := range states {
		if s.qi == len(q) && s.used >= 2 && (best == nil || s.cost() < best.cost()) {
			best = &s
		}
	}
	if best == nil {
		return nil
	}
	return best.idx
}
