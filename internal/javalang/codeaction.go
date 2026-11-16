package javalang

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
)

// ErrStale is a code action asked for a version of a document that the
// server no longer holds as its newest: the offsets it was asked with
// would mean another text.
var ErrStale = errors.New("javalang: the document changed since the request")

// TextEdit replaces the bytes Start to End of a file's text with NewText.
type TextEdit struct {
	Start, End int
	NewText    string
}

// FileEdit is the edits to one file, their offsets in the file's text as
// the server was asked about it.
type FileEdit struct {
	Path  string
	Edits []TextEdit
}

// Action is a fix or a refactoring the server offers at a range: its
// title, kind ("quickfix", "refactor.extract", "source.organizeImports")
// and the files it changes.
type Action struct {
	Title, Kind string
	Preferred   bool
	Edits       []FileEdit
}

// CodeActions asks what the server offers for the bytes start to end of
// path's text at version, with the diagnostics that touch them: jdtls
// reads a quick fix off the diagnostic's code, so they must come along.
// An action that does more than edit files, creates one or runs a command
// of the server's own, is left out, as this client only applies edits.
func (s *Session) CodeActions(path string, version uint64, start, end int, ds []Diagnostic) ([]Action, error) {
	s.mu.Lock()
	conn := s.conn
	doc := s.docs[path]
	s.mu.Unlock()
	if conn == nil {
		return nil, ErrNotStarted
	}
	if doc == nil {
		return nil, fmt.Errorf("javalang: %s is not open", path)
	}
	text := doc.newest()
	if text.Version() != version {
		return nil, ErrStale
	}
	rng := textRange{Start: positionAt(text, start), End: positionAt(text, end)}
	ctx := make([]codeDiagnostic, len(ds))
	for i, d := range ds {
		ctx[i] = codeDiagnostic{
			Range:    textRange{Start: positionAt(text, d.Start), End: positionAt(text, d.End)},
			Severity: lspSeverity(d.Severity),
			Message:  d.Message,
			Source:   d.Source,
			Code:     lspCode(d.Code),
		}
	}
	var raw json.RawMessage
	if err := conn.Call("textDocument/codeAction", codeActionParams{
		TextDocument: uriDocument{URI: fileURI(path)},
		Range:        rng,
		Context:      codeActionContext{Diagnostics: ctx},
	}, &raw); err != nil {
		return nil, err
	}
	var items []codeActionJSON
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("javalang: code action: %w", err)
	}
	var out []Action
	for _, it := range items {
		if a, ok := s.action(it); ok {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Preferred && !out[j].Preferred })
	return out, nil
}

// action turns an item into an Action, if the item is only edits: a
// CodeAction's own edit, or the java.apply.workspaceEdit command jdtls
// wraps its edits in, as a CodeAction's command or as a bare Command.
func (s *Session) action(it codeActionJSON) (Action, bool) {
	if it.Disabled != nil {
		return Action{}, false
	}
	edit := it.Edit
	if len(edit) == 0 || string(edit) == "null" {
		name, args := it.CommandName, it.Arguments
		if len(it.Command) > 0 {
			var c struct {
				Command   string            `json:"command"`
				Arguments []json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(it.Command, &c) != nil {
				return Action{}, false
			}
			name, args = c.Command, c.Arguments
		}
		if name != "java.apply.workspaceEdit" || len(args) == 0 {
			return Action{}, false
		}
		edit = args[0]
	}
	files, ok := s.workspaceEdit(edit)
	if !ok || len(files) == 0 {
		return Action{}, false
	}
	return Action{Title: it.Title, Kind: it.Kind, Preferred: it.IsPreferred, Edits: files}, true
}

// workspaceEdit reads the edits of a WorkspaceEdit in bytes of each file.
// A resource operation, a created or renamed file, makes it not ok.
func (s *Session) workspaceEdit(raw json.RawMessage) ([]FileEdit, bool) {
	var we struct {
		Changes         map[string][]textEdit `json:"changes"`
		DocumentChanges []json.RawMessage     `json:"documentChanges"`
	}
	if json.Unmarshal(raw, &we) != nil {
		return nil, false
	}
	byURI := map[string][]textEdit{}
	var order []string
	add := func(uri string, es []textEdit) {
		if _, seen := byURI[uri]; !seen {
			order = append(order, uri)
		}
		byURI[uri] = append(byURI[uri], es...)
	}
	for uri, es := range we.Changes {
		add(uri, es)
	}
	for _, r := range we.DocumentChanges {
		var dc struct {
			Kind         string `json:"kind"`
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []textEdit `json:"edits"`
		}
		if json.Unmarshal(r, &dc) != nil || dc.Kind != "" {
			return nil, false
		}
		add(dc.TextDocument.URI, dc.Edits)
	}
	sort.Strings(order)
	var out []FileEdit
	for _, uri := range order {
		path, ok := pathOfURI(uri)
		if !ok {
			return nil, false
		}
		text, ok := s.textOf(path)
		if !ok {
			return nil, false
		}
		fe := FileEdit{Path: path}
		for _, e := range byURI[uri] {
			r := e.Range
			if r == nil {
				r = e.Replace
			}
			if r == nil {
				return nil, false
			}
			fe.Edits = append(fe.Edits, TextEdit{
				Start:   offsetAt(text, r.Start.Line, r.Start.Character),
				End:     offsetAt(text, r.End.Line, r.End.Character),
				NewText: e.NewText,
			})
		}
		out = append(out, fe)
	}
	return out, true
}

// textOf is the text offsets of path count in: the open document's newest,
// else the file on disk.
func (s *Session) textOf(path string) (Text, bool) {
	s.mu.Lock()
	doc := s.docs[path]
	s.mu.Unlock()
	if doc != nil {
		return doc.newest(), true
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return newRawText(src), true
}

func lspSeverity(n int) int {
	// severity maps the server's 1..4 to 0..3; this is its way back.
	return n + 1
}

// lspCode is a diagnostic's code as the server sent it: jdtls's are
// numbers, and it reads a quoted one as a different code.
func lspCode(code string) any {
	if code == "" {
		return nil
	}
	if n, err := strconv.Atoi(code); err == nil {
		return n
	}
	return code
}

type codeActionParams struct {
	TextDocument uriDocument       `json:"textDocument"`
	Range        textRange         `json:"range"`
	Context      codeActionContext `json:"context"`
}

type codeActionContext struct {
	Diagnostics []codeDiagnostic `json:"diagnostics"`
}

type codeDiagnostic struct {
	Range    textRange `json:"range"`
	Severity int       `json:"severity,omitempty"`
	Code     any       `json:"code,omitempty"`
	Source   string    `json:"source,omitempty"`
	Message  string    `json:"message"`
}

// codeActionJSON is a CodeAction or a bare Command, which share one
// object: a CodeAction's "command" is an object, a Command's a string.
type codeActionJSON struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind"`
	IsPreferred bool            `json:"isPreferred"`
	Disabled    json.RawMessage `json:"disabled"`
	Edit        json.RawMessage `json:"edit"`
	// Command is a CodeAction's command; CommandName a Command's own, whose
	// arguments are Arguments.
	Command     json.RawMessage   `json:"-"`
	CommandName string            `json:"-"`
	Arguments   []json.RawMessage `json:"arguments"`
}

func (c *codeActionJSON) UnmarshalJSON(b []byte) error {
	type plain codeActionJSON
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var f struct {
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*c = codeActionJSON(p)
	if string(c.Disabled) == "null" {
		c.Disabled = nil
	}
	var name string
	if json.Unmarshal(f.Command, &name) == nil && name != "" {
		c.CommandName = name
	} else if len(f.Command) > 0 && string(f.Command) != "null" {
		c.Command = f.Command
	}
	return nil
}
