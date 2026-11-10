package dap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// Breakpoints are a project's breakpoints: the lines of each file, named
// relative to the project with slashes, saved so that a session starts with
// the ones the last one had.
type Breakpoints struct {
	files map[string][]int
}

type breakpointFile struct {
	Files map[string][]int `json:"files"`
}

// NewBreakpoints is an empty set.
func NewBreakpoints() *Breakpoints { return &Breakpoints{files: map[string][]int{}} }

// LoadBreakpoints reads path. A missing file yields an empty set and no
// error.
func LoadBreakpoints(path string) (*Breakpoints, error) {
	b := NewBreakpoints()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	var f breakpointFile
	if err := json.Unmarshal(data, &f); err != nil {
		return b, err
	}
	for rel, lines := range f.Files {
		for _, l := range lines {
			if l > 0 {
				b.files[rel] = append(b.files[rel], l)
			}
		}
		sort.Ints(b.files[rel])
	}
	return b, nil
}

// Save is atomic: a failed write leaves the previous file intact.
func (b *Breakpoints) Save(path string) error {
	data, err := json.MarshalIndent(breakpointFile{Files: b.files}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Toggle sets or clears one line, and reports whether it is set now.
func (b *Breakpoints) Toggle(rel string, line int) bool {
	lines := b.files[rel]
	i := sort.SearchInts(lines, line)
	if i < len(lines) && lines[i] == line {
		lines = append(lines[:i], lines[i+1:]...)
		if len(lines) == 0 {
			delete(b.files, rel)
		} else {
			b.files[rel] = lines
		}
		return false
	}
	b.files[rel] = append(lines, 0)
	copy(b.files[rel][i+1:], b.files[rel][i:])
	b.files[rel][i] = line
	return true
}

// Lines are one file's breakpoints, ascending, 1-based.
func (b *Breakpoints) Lines(rel string) []int { return append([]int(nil), b.files[rel]...) }

// Set makes lines the file's breakpoints, replacing those it had, as an
// edit that moved them does.
func (b *Breakpoints) Set(rel string, lines []int) {
	if len(lines) == 0 {
		delete(b.files, rel)
		return
	}
	out := append([]int(nil), lines...)
	sort.Ints(out)
	b.files[rel] = slicesCompact(out)
}

// Files are the files that have breakpoints, in order.
func (b *Breakpoints) Files() []string {
	out := make([]string, 0, len(b.files))
	for rel := range b.files {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// Empty reports whether no file has a breakpoint.
func (b *Breakpoints) Empty() bool { return len(b.files) == 0 }

// Remap keeps breakpoints on the lines they were set on as the text is
// edited: lines above the change stay, lines below it shift by the lines
// added or removed, and a line inside it stays where it was while the text
// still has that line. Both texts are the file's lines, unmoved last.
func Remap(old, new []string, lines []int) []int {
	if len(lines) == 0 {
		return nil
	}
	if len(new) == 0 {
		return nil
	}
	head := 0
	for head < len(old) && head < len(new) && old[head] == new[head] {
		head++
	}
	tail := 0
	for tail < len(old)-head && tail < len(new)-head &&
		old[len(old)-1-tail] == new[len(new)-1-tail] {
		tail++
	}
	delta := len(new) - len(old)
	out := make([]int, 0, len(lines))
	for _, l := range lines {
		i := l - 1
		switch {
		case i < head:
			out = append(out, l)
		case i >= len(old)-tail:
			out = append(out, l+delta)
		default:
			// Inside the change: the same line when it still exists.
			out = append(out, min(i, len(new)-1)+1)
		}
	}
	sort.Ints(out)
	out = slicesCompact(out)
	return out
}

// slicesCompact drops the neighbours Remap made equal.
func slicesCompact(in []int) []int {
	out := in[:0]
	last := -1
	for _, v := range in {
		if v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}
