// Package javalang is the editor's side of jdtls: it keeps the server's
// documents in step with the editor's, reads what the server publishes in
// the bytes of the versions they describe, and turns feature requests into
// byte offsets of the editor's text.
package javalang

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"megafactory/internal/lsp"
)

// Text is one version of a document as the editor holds it: its bytes, its
// lines, and the version the server knows it by. It is read, never written,
// so a text stays valid as later versions arrive.
type Text interface {
	Version() uint64
	Len() int
	LineOf(offset int) int
	LineStart(line int) int
	Slice(start, end int) string
}

// Diagnostic is a problem the server reports, its range in byte offsets of
// the text it was reported for.
type Diagnostic struct {
	Start, End int
	Severity   int
	Message    string
	Source     string
	Code       string
}

// Status is how far the server is in bringing the project up.
type Status int

const (
	StatusStarting Status = iota
	StatusImporting
	StatusReady
	StatusError
)

// Item is a completion the server proposes. Insert is the text to put in,
// Snippet saying whether it holds snippet placeholders.
type Item struct {
	Label, Detail, Insert, Documentation, FilterText string
	Kind                                             int
	Snippet                                          bool
}

// Location is a definition's target: a file and a range in its bytes.
type Location struct {
	Path       string
	Start, End int
}

// ErrNotStarted is a call that needs the server before Attach connected
// the session to one.
var ErrNotStarted = errors.New("javalang: session is not attached")

// history is how many versions of a document stay readable: diagnostics
// arrive for a version that is no longer the newest, and their ranges
// only mean something against the text they were written for.
const history = 32

// Session is the editor's link to jdtls. It is the lsp.Handler the server
// talks to, so it can be given to jdtls.Start before a connection exists,
// and its calls are safe from any goroutine.
type Session struct {
	// mu guards everything below, and never covers a write to the server.
	mu          sync.Mutex
	conn        *lsp.Conn
	incremental bool
	docs        map[string]*document
	diagnostics func(path string, version uint64, ds []Diagnostic)
	status      func(Status, string)
	// impls are the answers to Implementation, kept as the server takes
	// ~200 ms to give them again; see Implementation for when they go.
	impls    map[implKey]implAnswer
	implsGen uint64

	// wmu orders the document notifications: a change must follow the open
	// it belongs to and precede the next one, whatever goroutine sends them.
	wmu sync.Mutex
}

type implKey struct {
	path   string
	offset int
}

type implAnswer struct {
	locs []Location
	at   time.Time
}

// implementationTTL bounds how long an answer is kept: a file changed
// behind the editor's back, a checkout, is not seen by any change below.
const implementationTTL = 2 * time.Minute

// document is one open file and its versions, oldest first. A change makes
// a new document rather than amending this one, so a reader holding it sees
// one version's history.
type document struct {
	texts []Text
}

func (d *document) newest() Text { return d.texts[len(d.texts)-1] }

func (d *document) find(version uint64) Text {
	for i := len(d.texts) - 1; i >= 0; i-- {
		if d.texts[i].Version() == version {
			return d.texts[i]
		}
	}
	return nil
}

func (d *document) with(text Text) *document {
	from := 0
	if len(d.texts) >= history {
		from = len(d.texts) - (history - 1)
	}
	texts := make([]Text, 0, history)
	texts = append(texts, d.texts[from:]...)
	texts = append(texts, text)
	return &document{texts: texts}
}

// New is a session with no server yet. Attach connects it after
// jdtls.Start has completed.
func New() *Session {
	return &Session{docs: map[string]*document{}}
}

// Attach connects the session to the server's connection, with the raw
// capabilities of the initialize result. Only an incremental
// textDocumentSync.change sends a range; an absent, None (0) or Full (1)
// one sends the whole text.
func (s *Session) Attach(conn *lsp.Conn, capabilities []byte) {
	incremental := changeKind(capabilities) == 2
	s.mu.Lock()
	s.conn = conn
	s.incremental = incremental
	s.mu.Unlock()
}

func changeKind(capabilities []byte) int {
	var caps struct {
		TextDocumentSync json.RawMessage `json:"textDocumentSync"`
	}
	if json.Unmarshal(capabilities, &caps) != nil {
		return 1
	}
	raw := bytes.TrimSpace(caps.TextDocumentSync)
	if len(raw) == 0 || string(raw) == "null" {
		return 1
	}
	if raw[0] == '{' {
		var flags struct {
			Change *int `json:"change"`
		}
		if json.Unmarshal(raw, &flags) != nil || flags.Change == nil {
			return 1
		}
		return *flags.Change
	}
	var kind int
	if json.Unmarshal(raw, &kind) != nil {
		return 1
	}
	return kind
}

// SetHandlers says where diagnostics and status changes go. Both are
// called on the connection's reading goroutine, so they must not wait
// for the server themselves.
func (s *Session) SetHandlers(diagnostics func(path string, version uint64, ds []Diagnostic), status func(Status, string)) {
	s.mu.Lock()
	s.diagnostics = diagnostics
	s.status = status
	s.mu.Unlock()
}

// Open sends a document whole, at its version, and starts its history.
func (s *Session) Open(path, languageID string, text Text) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	conn, err := s.connection()
	if err != nil {
		return err
	}
	if text == nil {
		return errors.New("javalang: no text")
	}
	params := didOpenParams{TextDocument: documentItem{
		URI:        fileURI(path),
		LanguageID: languageID,
		Version:    text.Version(),
		Text:       text.Slice(0, text.Len()),
	}}
	// The state goes in first: a server that answers the open at once may
	// publish against the new text before this call returns.
	s.mu.Lock()
	previous := s.docs[path]
	if s.docs == nil {
		s.docs = map[string]*document{}
	}
	s.docs[path] = &document{texts: []Text{text}}
	s.mu.Unlock()
	if err := conn.Notify("textDocument/didOpen", params); err != nil {
		s.mu.Lock()
		if previous == nil {
			delete(s.docs, path)
		} else {
			s.docs[path] = previous
		}
		s.mu.Unlock()
		return err
	}
	return nil
}

// Change sends the one edit between the stored text and text. A text whose
// version is not newer is ignored, and so is one whose bytes match: only
// the history gains it.
func (s *Session) Change(path string, text Text) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	conn := s.conn
	doc := s.docs[path]
	incremental := s.incremental
	s.mu.Unlock()
	if conn == nil {
		return ErrNotStarted
	}
	if text == nil {
		return errors.New("javalang: no text")
	}
	if doc == nil {
		return fmt.Errorf("javalang: %s is not open", path)
	}
	old := doc.newest()
	if text.Version() <= old.Version() {
		return nil
	}
	start, end, replacement, changed := diff(old, text)
	if !changed {
		s.mu.Lock()
		s.docs[path] = doc.with(text)
		s.mu.Unlock()
		return nil
	}
	change := contentChange{Text: replacement}
	if incremental {
		change.Range = &textRange{Start: positionAt(old, start), End: positionAt(old, end)}
	} else {
		change.Text = text.Slice(0, text.Len())
	}
	params := didChangeParams{
		TextDocument:   versionedDocument{URI: fileURI(path), Version: text.Version()},
		ContentChanges: []contentChange{change},
	}
	// As with Open: install the text first, so a diagnostic for the new
	// version that overtakes the answer is read against its own bytes.
	s.mu.Lock()
	s.docs[path] = doc.with(text)
	s.dropImplementations()
	s.mu.Unlock()
	if err := conn.Notify("textDocument/didChange", params); err != nil {
		s.mu.Lock()
		s.docs[path] = doc
		s.mu.Unlock()
		return err
	}
	return nil
}

// Save tells the server the document was written as it is.
func (s *Session) Save(path string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	conn, err := s.connection()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.dropImplementations()
	s.mu.Unlock()
	return conn.Notify("textDocument/didSave", didSaveParams{TextDocument: uriDocument{URI: fileURI(path)}})
}

// Close closes the document and drops what the session knew about it.
func (s *Session) Close(path string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	conn, err := s.connection()
	if err != nil {
		return err
	}
	err = conn.Notify("textDocument/didClose", didCloseParams{TextDocument: uriDocument{URI: fileURI(path)}})
	s.mu.Lock()
	delete(s.docs, path)
	s.mu.Unlock()
	return err
}

// Complete asks for completions at offset and returns where they start, in
// bytes. An item's own edit range wins over itemDefaults.editRange; without
// either the start is the word before offset.
func (s *Session) Complete(path string, offset int) (int, []Item, error) {
	s.mu.Lock()
	conn := s.conn
	doc := s.docs[path]
	s.mu.Unlock()
	if conn == nil {
		return 0, nil, ErrNotStarted
	}
	if doc == nil {
		return 0, nil, fmt.Errorf("javalang: %s is not open", path)
	}
	text := doc.newest()
	var raw json.RawMessage
	if err := conn.Call("textDocument/completion", positionParams{
		TextDocument: uriDocument{URI: fileURI(path)},
		Position:     positionAt(text, offset),
	}, &raw); err != nil {
		return 0, nil, err
	}
	var (
		items    []completionItem
		defaults *editRange
	)
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "null":
	case raw[0] == '[':
		if err := json.Unmarshal(raw, &items); err != nil {
			return 0, nil, fmt.Errorf("javalang: completion: %w", err)
		}
	case raw[0] == '{':
		var list struct {
			Items        []completionItem `json:"items"`
			ItemDefaults struct {
				EditRange json.RawMessage `json:"editRange"`
			} `json:"itemDefaults"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return 0, nil, fmt.Errorf("javalang: completion: %w", err)
		}
		items = list.Items
		defaults = editRangeOf(list.ItemDefaults.EditRange)
	}
	start := -1
	if len(items) > 0 {
		if p, ok := items[0].start(defaults); ok {
			start = offsetAt(text, p.Line, p.Character)
		}
	}
	if start < 0 {
		start = wordStart(text, offset)
	}
	out := make([]Item, 0, len(items))
	for i := range items {
		it := &items[i]
		edit, _ := it.edit()
		insert := edit.NewText
		if insert == "" {
			insert = it.InsertText
		}
		snippet := it.InsertTextFormat == 2 || hasSnippet(insert)
		out = append(out, Item{
			Label:         it.Label,
			Detail:        it.Detail,
			Insert:        insert,
			Documentation: markup(it.Documentation),
			FilterText:    it.FilterText,
			Kind:          it.Kind,
			Snippet:       snippet,
		})
	}
	return start, out, nil
}

// Hover asks what the server knows at offset. The returned range is the
// server's when it gives one, else the offset alone.
func (s *Session) Hover(path string, offset int) (int, int, string, error) {
	s.mu.Lock()
	conn := s.conn
	doc := s.docs[path]
	s.mu.Unlock()
	if conn == nil {
		return 0, 0, "", ErrNotStarted
	}
	if doc == nil {
		return 0, 0, "", fmt.Errorf("javalang: %s is not open", path)
	}
	text := doc.newest()
	var res struct {
		Contents json.RawMessage `json:"contents"`
		Range    *textRange      `json:"range"`
	}
	if err := conn.Call("textDocument/hover", positionParams{
		TextDocument: uriDocument{URI: fileURI(path)},
		Position:     positionAt(text, offset),
	}, &res); err != nil {
		return 0, 0, "", err
	}
	start, end := offset, offset
	if res.Range != nil {
		start = offsetAt(text, res.Range.Start.Line, res.Range.Start.Character)
		end = offsetAt(text, res.Range.End.Line, res.Range.End.Character)
	}
	return start, end, hoverText(res.Contents), nil
}

// Definition asks where the thing at offset is defined. Links and plain
// locations are both accepted; anything not on disk is dropped.
func (s *Session) Definition(path string, offset int) ([]Location, error) {
	return s.locationsAt("textDocument/definition", path, offset)
}

// Implementation asks where the method or type at offset is implemented:
// the overriding methods of an interface's or abstract method, the
// subtypes of a type. Anything not on disk is dropped.
//
// jdtls takes ~200 ms for it every time, and ~1 min the first, so an answer
// is kept until a change, a save or a build file's change (any of which may
// add or remove an implementation, in any file), or implementationTTL.
func (s *Session) Implementation(path string, offset int) ([]Location, error) {
	key := implKey{path, offset}
	s.mu.Lock()
	ans, ok := s.impls[key]
	gen := s.implsGen
	s.mu.Unlock()
	if ok && time.Since(ans.at) < implementationTTL {
		return slices.Clone(ans.locs), nil
	}
	locs, err := s.locationsAt("textDocument/implementation", path, offset)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// An answer that began before a change may be of the text before it.
	if gen == s.implsGen {
		if s.impls == nil {
			s.impls = map[implKey]implAnswer{}
		}
		s.impls[key] = implAnswer{slices.Clone(locs), time.Now()}
	}
	s.mu.Unlock()
	return locs, nil
}

// dropImplementations forgets the kept answers; s.mu is held.
func (s *Session) dropImplementations() {
	clear(s.impls)
	s.implsGen++
}

func (s *Session) locationsAt(method, path string, offset int) ([]Location, error) {
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
	var raw json.RawMessage
	if err := conn.Call(method, positionParams{
		TextDocument: uriDocument{URI: fileURI(path)},
		Position:     positionAt(doc.newest(), offset),
	}, &raw); err != nil {
		return nil, err
	}
	return s.locations(raw), nil
}

// BuildFilesChanged tells the server a build file changed, which is how
// jdtls re-imports a project: its command list has no update command.
func (s *Session) BuildFilesChanged(paths ...string) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return ErrNotStarted
	}
	if len(paths) == 0 {
		return nil
	}
	s.mu.Lock()
	s.dropImplementations()
	s.mu.Unlock()
	changes := make([]fileChange, len(paths))
	for i, path := range paths {
		changes[i] = fileChange{URI: fileURI(path), Type: 2}
	}
	return conn.Notify("workspace/didChangeWatchedFiles", watchedFiles{Changes: changes})
}

func (s *Session) connection() (*lsp.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, ErrNotStarted
	}
	return s.conn, nil
}

// Notify is the server's half of lsp.Handler: the messages it publishes.
func (s *Session) Notify(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		s.publish(params)
	case "language/status":
		s.languageStatus(params)
	}
}

// Request is the server's half of lsp.Handler: the questions it asks the
// client, answered with what a client with no settings can.
func (s *Session) Request(method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(params, &p) != nil {
			return nil, nil
		}
		return make([]any, len(p.Items)), nil
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest":
		return nil, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false, "failureReason": "megafactory applies no workspace edits"}, nil
	}
	return nil, nil
}

func (s *Session) publish(raw json.RawMessage) {
	var p struct {
		URI         string  `json:"uri"`
		Version     *uint64 `json:"version"`
		Diagnostics []struct {
			Range    textRange       `json:"range"`
			Severity int             `json:"severity"`
			Message  string          `json:"message"`
			Source   string          `json:"source"`
			Code     json.RawMessage `json:"code"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	path, ok := pathOfURI(p.URI)
	if !ok {
		return
	}
	s.mu.Lock()
	doc := s.docs[path]
	var (
		text    Text
		version uint64
	)
	if doc != nil {
		text = doc.newest()
		if p.Version != nil {
			if v := doc.find(*p.Version); v != nil {
				text, version = v, *p.Version
			}
		}
	}
	handler := s.diagnostics
	s.mu.Unlock()
	if text == nil || handler == nil {
		return
	}
	ds := make([]Diagnostic, len(p.Diagnostics))
	for i, d := range p.Diagnostics {
		ds[i] = Diagnostic{
			Start:    offsetAt(text, d.Range.Start.Line, d.Range.Start.Character),
			End:      offsetAt(text, d.Range.End.Line, d.Range.End.Character),
			Severity: severity(d.Severity),
			Message:  d.Message,
			Source:   d.Source,
			Code:     codeString(d.Code),
		}
	}
	handler(path, version, ds)
}

func (s *Session) languageStatus(raw json.RawMessage) {
	var p struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	var status Status
	switch p.Type {
	case "Starting", "Started":
		status = StatusStarting
	case "Message":
		status = StatusImporting
	case "ServiceReady":
		status = StatusReady
	case "ProjectStatus":
		// WARNING while the import runs, OK when it is done; any other
		// word still means the project is not ready yet.
		if p.Message == "OK" {
			status = StatusReady
		} else {
			status = StatusImporting
		}
	case "Error":
		status = StatusError
	default:
		return
	}
	s.mu.Lock()
	handler := s.status
	s.mu.Unlock()
	if handler != nil {
		handler(status, p.Message)
	}
}

// severity maps LSP's Error..Hint, 1..4, to the editor's 0..3.
func severity(n int) int {
	if n >= 1 && n <= 4 {
		return n - 1
	}
	return 0
}

// codeString reads a diagnostic code, which the protocol allows as either
// a string or a number.
func codeString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// locations reads what a definition returned: a location, a list of them,
// or a list of location links.
func (s *Session) locations(raw json.RawMessage) []Location {
	raw = bytes.TrimSpace(raw)
	var raws []json.RawMessage
	switch {
	case len(raw) == 0 || string(raw) == "null":
		return nil
	case raw[0] == '[':
		if json.Unmarshal(raw, &raws) != nil {
			return nil
		}
	case raw[0] == '{':
		raws = []json.RawMessage{raw}
	default:
		return nil
	}
	var out []Location
	for _, r := range raws {
		var l locationJSON
		if json.Unmarshal(r, &l) != nil {
			continue
		}
		uri, span := l.URI, l.Range
		if l.TargetURI != "" {
			uri, span = l.TargetURI, l.TargetRange
		}
		if uri == "" || span == nil {
			continue
		}
		path, ok := pathOfURI(uri)
		if !ok {
			continue
		}
		start, end, ok := s.span(path, *span)
		if !ok {
			continue
		}
		out = append(out, Location{Path: path, Start: start, End: end})
	}
	return out
}

// span converts a range to byte offsets: of the document's newest text if
// it is open, else of the file on disk, as a definition may point anywhere.
func (s *Session) span(path string, r textRange) (int, int, bool) {
	s.mu.Lock()
	doc := s.docs[path]
	s.mu.Unlock()
	var text Text
	if doc != nil {
		text = doc.newest()
	} else {
		src, err := os.ReadFile(path)
		if err != nil {
			return 0, 0, false
		}
		text = newRawText(src)
	}
	return offsetAt(text, r.Start.Line, r.Start.Character), offsetAt(text, r.End.Line, r.End.Character), true
}

// diff is the one edit that turns old into new: the range in old and the
// text that replaces it. Lines are compared whole, newlines included, so
// equal lines start at equal bytes; the ends of the differing block then
// shrink to the differing runes.
func diff(old, new Text) (start, end int, replacement string, changed bool) {
	oldLines, newLines := lineCount(old), lineCount(new)
	first := 0
	for first < oldLines && first < newLines && sameLine(old, first, new, first) {
		first++
	}
	if first == oldLines && first == newLines {
		return 0, 0, "", false
	}
	lastOld, lastNew := oldLines-1, newLines-1
	for lastOld >= first && lastNew >= first && sameLine(old, lastOld, new, lastNew) {
		lastOld--
		lastNew--
	}
	oldStart, oldEnd := lineStart(old, first), lineStart(old, lastOld+1)
	newStart, newEnd := lineStart(new, first), lineStart(new, lastNew+1)
	before, after := old.Slice(oldStart, oldEnd), new.Slice(newStart, newEnd)

	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	// A shared prefix can end inside a rune whose bytes differ later: send
	// whole runes, as positions are counted in UTF-16 code units.
	prefix = runeStart(before, prefix)

	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	// Shared suffix bytes are aligned to the end; move its start right
	// until it begins a rune, so no edit splits one.
	endOff := len(before) - suffix
	for endOff < len(before) && before[endOff]&0xC0 == 0x80 {
		endOff++
	}
	suffix = len(before) - endOff
	replacement = after[prefix : len(after)-suffix]
	return oldStart + prefix, oldStart + endOff, replacement, true
}

// runeStart backs i off a continuation byte to the rune holding it.
func runeStart(s string, i int) int {
	for i > 0 && i < len(s) && s[i]&0xC0 == 0x80 {
		i--
	}
	return i
}

func lineCount(t Text) int {
	n := t.LineOf(t.Len())
	if n < 0 {
		n = 0
	}
	return n + 1
}

func lineStart(t Text, line int) int {
	n := t.LineStart(line)
	if n < 0 {
		return 0
	}
	if n > t.Len() {
		return t.Len()
	}
	return n
}

func sameLine(a Text, i int, b Text, j int) bool {
	a0, a1 := lineStart(a, i), lineStart(a, i+1)
	b0, b1 := lineStart(b, j), lineStart(b, j+1)
	if a1-a0 != b1-b0 {
		return false
	}
	return a.Slice(a0, a1) == b.Slice(b0, b1)
}

// positionAt is the LSP position, in UTF-16 code units, of a byte offset.
func positionAt(t Text, off int) position {
	if off < 0 {
		off = 0
	}
	if off > t.Len() {
		off = t.Len()
	}
	line := t.LineOf(off)
	if line < 0 {
		line = 0
	}
	start := lineStart(t, line)
	if start > off {
		start = off
	}
	return position{Line: line, Character: utf16Len(t.Slice(start, off))}
}

// offsetAt is the byte offset of an LSP position. The column counts UTF-16
// code units, and a column past the line stops at its end, before the
// newline, as the protocol asks.
func offsetAt(t Text, line, character int) int {
	if line < 0 {
		line = 0
	}
	if character < 0 {
		character = 0
	}
	start := lineStart(t, line)
	end := lineStart(t, line+1)
	if start > end {
		start = end
	}
	s := t.Slice(start, end)
	if n := len(s); n > 0 && s[n-1] == '\n' {
		n--
		if n > 0 && s[n-1] == '\r' {
			n--
		}
		s = s[:n]
	}
	units := 0
	for i, r := range s {
		if units >= character || units+utf16.RuneLen(r) > character {
			return start + i
		}
		units += utf16.RuneLen(r)
	}
	return start + len(s)
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// wordStart is where the identifier, dotted name or variable before offset
// begins: its letters, digits, _ and $.
func wordStart(t Text, off int) int {
	if off < 0 {
		off = 0
	}
	if off > t.Len() {
		off = t.Len()
	}
	prefix := t.Slice(lineStart(t, t.LineOf(off)), off)
	for len(prefix) > 0 {
		r, size := utf8.DecodeLastRuneInString(prefix)
		if !wordRune(r) {
			break
		}
		off -= size
		prefix = prefix[:len(prefix)-size]
	}
	return off
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$'
}

// hasSnippet reports whether text holds a snippet placeholder, like $1 or
// ${1:name}, which is how a server that did not say so still sends them.
func hasSnippet(text string) bool {
	for i := range len(text) {
		if text[i] != '$' || (i > 0 && text[i-1] == '\\') || i+1 == len(text) {
			continue
		}
		if c := text[i+1]; c == '{' || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

// hoverText joins what a hover holds: MarkupContent, a MarkedString, or an
// array of either, with a blank line between them.
func hoverText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case '{':
		return markup(raw)
	case '[':
		var parts []json.RawMessage
		if json.Unmarshal(raw, &parts) != nil {
			return ""
		}
		var values []string
		for _, part := range parts {
			if v := hoverText(part); v != "" {
				values = append(values, v)
			}
		}
		return strings.Join(values, "\n\n")
	}
	return ""
}

// markup reads a value that is either a string or {value: string}: a
// MarkupContent, a MarkedString object, or documentation.
func markup(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var v struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v.Value
}

// fileURI is the URI of an absolute path, as jdtls names files.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// pathOfURI is the absolute path of a file URI; other schemes have none.
func pathOfURI(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Path == "" {
		return "", false
	}
	path := u.Path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), true
}

// rawText is the text of a file read for a definition target the editor
// has not opened.
type rawText struct {
	src    []byte
	starts []int
}

func newRawText(src []byte) *rawText {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &rawText{src: src, starts: starts}
}

func (t *rawText) Version() uint64 { return 0 }
func (t *rawText) Len() int        { return len(t.src) }

func (t *rawText) LineOf(off int) int {
	if off <= 0 {
		return 0
	}
	if off > len(t.src) {
		off = len(t.src)
	}
	return sort.Search(len(t.starts), func(i int) bool { return t.starts[i] > off }) - 1
}

func (t *rawText) LineStart(line int) int {
	if line <= 0 {
		return 0
	}
	if line >= len(t.starts) {
		return len(t.src)
	}
	return t.starts[line]
}

func (t *rawText) Slice(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(t.src) {
		end = len(t.src)
	}
	if start >= end {
		return ""
	}
	return string(t.src[start:end])
}

// position is an LSP position: a line and a UTF-16 column in it.
type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type textRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type uriDocument struct {
	URI string `json:"uri"`
}

type versionedDocument struct {
	URI     string `json:"uri"`
	Version uint64 `json:"version"`
}

type documentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    uint64 `json:"version"`
	Text       string `json:"text"`
}

type didOpenParams struct {
	TextDocument documentItem `json:"textDocument"`
}

type contentChange struct {
	Range *textRange `json:"range,omitempty"`
	Text  string     `json:"text"`
}

type didChangeParams struct {
	TextDocument   versionedDocument `json:"textDocument"`
	ContentChanges []contentChange   `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument uriDocument `json:"textDocument"`
}

type didCloseParams struct {
	TextDocument uriDocument `json:"textDocument"`
}

type positionParams struct {
	TextDocument uriDocument `json:"textDocument"`
	Position     position    `json:"position"`
}

type watchedFiles struct {
	Changes []fileChange `json:"changes"`
}

type fileChange struct {
	URI  string `json:"uri"`
	Type int    `json:"type"`
}

// completionItem is the part of a completion this client reads.
type completionItem struct {
	Label            string          `json:"label"`
	Detail           string          `json:"detail"`
	Kind             int             `json:"kind"`
	FilterText       string          `json:"filterText"`
	InsertText       string          `json:"insertText"`
	InsertTextFormat int             `json:"insertTextFormat"`
	TextEdit         json.RawMessage `json:"textEdit"`
	Documentation    json.RawMessage `json:"documentation"`
}

func (it *completionItem) edit() (textEdit, bool) {
	if len(bytes.TrimSpace(it.TextEdit)) == 0 {
		return textEdit{}, false
	}
	var e textEdit
	if json.Unmarshal(it.TextEdit, &e) != nil {
		return textEdit{}, false
	}
	return e, true
}

// start is where the item's completion begins: its own edit range, else
// the list's default. A range wins over an insert/replace pair.
func (it *completionItem) start(defaults *editRange) (position, bool) {
	if e, ok := it.edit(); ok {
		if p, ok := e.start(); ok {
			return p, true
		}
	}
	if defaults != nil {
		return defaults.start()
	}
	return position{}, false
}

type textEdit struct {
	NewText string     `json:"newText"`
	Range   *textRange `json:"range"`
	Insert  *textRange `json:"insert"`
	Replace *textRange `json:"replace"`
}

func (e textEdit) start() (position, bool) {
	r := editRange{Range: e.Range, Insert: e.Insert, Replace: e.Replace}
	return r.start()
}

// editRange is a range, or the insert/replace pair a server sends once the
// client says it supports replacing more than it inserts.
type editRange struct {
	Range   *textRange `json:"range"`
	Insert  *textRange `json:"insert"`
	Replace *textRange `json:"replace"`
}

func (e *editRange) start() (position, bool) {
	switch {
	case e == nil:
	case e.Range != nil:
		return e.Range.Start, true
	case e.Insert != nil:
		return e.Insert.Start, true
	case e.Replace != nil:
		return e.Replace.Start, true
	}
	return position{}, false
}

func editRangeOf(raw json.RawMessage) *editRange {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	// itemDefaults.editRange is a range, or an insert/replace pair.
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	if _, ok := fields["start"]; ok {
		var r textRange
		if json.Unmarshal(raw, &r) != nil {
			return nil
		}
		return &editRange{Range: &r}
	}
	var e editRange
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	if e.Range == nil && e.Insert == nil && e.Replace == nil {
		return nil
	}
	return &e
}

// locationJSON is a Location or a LocationLink, whose fields differ only in
// their names.
type locationJSON struct {
	URI         string     `json:"uri"`
	TargetURI   string     `json:"targetUri"`
	Range       *textRange `json:"range"`
	TargetRange *textRange `json:"targetRange"`
}
