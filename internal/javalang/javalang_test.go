package javalang

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"megafactory/internal/lsp"
)

// text is one document version, as a rope snapshot would present it.
type text struct {
	version uint64
	body    string
	starts  []int
}

func newText(version uint64, body string) *text {
	starts := []int{0}
	for i := range len(body) {
		if body[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &text{version: version, body: body, starts: starts}
}

func (t *text) Version() uint64 { return t.version }
func (t *text) Len() int        { return len(t.body) }

func (t *text) LineOf(off int) int {
	if off <= 0 {
		return 0
	}
	if off > len(t.body) {
		off = len(t.body)
	}
	line := 0
	for line+1 < len(t.starts) && t.starts[line+1] <= off {
		line++
	}
	return line
}

func (t *text) LineStart(line int) int {
	if line <= 0 {
		return 0
	}
	if line >= len(t.starts) {
		return len(t.body)
	}
	return t.starts[line]
}

func (t *text) Slice(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(t.body) {
		end = len(t.body)
	}
	if start >= end {
		return ""
	}
	return t.body[start:end]
}

// note is one message the fake server saw, notification or request.
type note struct {
	method string
	params json.RawMessage
}

// fake is the server's end: it records what the session sends and answers
// requests with whatever the current test set up.
type fake struct {
	calls chan note
	mu    sync.Mutex
	reply func(method string, params json.RawMessage) any
}

func (f *fake) Notify(method string, params json.RawMessage) {
	f.calls <- note{method: method, params: params}
}

func (f *fake) Request(method string, params json.RawMessage) (any, error) {
	f.calls <- note{method: method, params: params}
	f.mu.Lock()
	reply := f.reply
	f.mu.Unlock()
	if reply == nil {
		return nil, nil
	}
	return reply(method, params), nil
}

func (f *fake) respond(reply func(method string, params json.RawMessage) any) {
	f.mu.Lock()
	f.reply = reply
	f.mu.Unlock()
}

type harness struct {
	session *Session
	remote  *lsp.Conn
	fake    *fake
}

func newHarness(t *testing.T, capabilities string) *harness {
	t.Helper()
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		c1.Close()
		c2.Close()
	})
	f := &fake{calls: make(chan note, 64)}
	remote := lsp.NewConn(c2, c2, f)
	session := New()
	client := lsp.NewConn(c1, c1, session)
	session.Attach(client, []byte(capabilities))
	return &harness{session: session, remote: remote, fake: f}
}

func (h *harness) next(t *testing.T) note {
	t.Helper()
	select {
	case n := <-h.fake.calls:
		return n
	case <-time.After(2 * time.Second):
		t.Fatal("the server saw no message")
		return note{}
	}
}

func TestOpenSendsDidOpen(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	if err := h.session.Open(path, "java", newText(1, "class A {\n}\n")); err != nil {
		t.Fatal(err)
	}
	n := h.next(t)
	if n.method != "textDocument/didOpen" {
		t.Fatalf("method %s", n.method)
	}
	var p struct {
		TextDocument struct {
			URI        string `json:"uri"`
			LanguageID string `json:"languageId"`
			Version    uint64 `json:"version"`
			Text       string `json:"text"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(n.params, &p); err != nil {
		t.Fatal(err)
	}
	d := p.TextDocument
	if d.URI != fileURI(path) || d.LanguageID != "java" || d.Version != 1 || d.Text != "class A {\n}\n" {
		t.Errorf("didOpen %+v", d)
	}
}

func TestChangeSendsOneIncrementalRange(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	oldBody := "// 😀 café\nclass A {\n    String s = \"😀é\";\n}\n"
	if err := h.session.Open(path, "java", newText(1, oldBody)); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	newBody := "// 😀 café\nclass A {\n    String s = \"😀é\" + 1;\n}\n"
	if err := h.session.Change(path, newText(2, newBody)); err != nil {
		t.Fatal(err)
	}
	n := h.next(t)
	if n.method != "textDocument/didChange" {
		t.Fatalf("method %s", n.method)
	}
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version uint64 `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Range *struct {
				Start position `json:"start"`
				End   position `json:"end"`
			} `json:"range"`
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(n.params, &p); err != nil {
		t.Fatal(err)
	}
	if p.TextDocument.URI != fileURI(path) || p.TextDocument.Version != 2 || len(p.ContentChanges) != 1 {
		t.Fatalf("didChange %+v", p)
	}
	c := p.ContentChanges[0]
	// The 😀 and é before the edit are what the column must not count as
	// bytes: both are one byte further than their UTF-16 width.
	want := position{Line: 2, Character: 20}
	if c.Range == nil || c.Range.Start != want || c.Range.End != want || c.Text != " + 1" {
		t.Errorf("change %+v", c)
	}
}

func TestChangeSendsWholeTextWithoutIncrementalSync(t *testing.T) {
	for _, tc := range []struct{ name, capabilities string }{
		{"full", `{"textDocumentSync":{"change":1}}`},
		{"absent", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.capabilities)
			path := "/tmp/A.java"
			h.session.Open(path, "java", newText(1, "one\n"))
			h.next(t)
			if err := h.session.Change(path, newText(2, "two\n")); err != nil {
				t.Fatal(err)
			}
			n := h.next(t)
			var p struct {
				ContentChanges []struct {
					Range *textRange `json:"range"`
					Text  string     `json:"text"`
				} `json:"contentChanges"`
			}
			if err := json.Unmarshal(n.params, &p); err != nil {
				t.Fatal(err)
			}
			if len(p.ContentChanges) != 1 || p.ContentChanges[0].Range != nil || p.ContentChanges[0].Text != "two\n" {
				t.Errorf("change %+v", p.ContentChanges)
			}
		})
	}
}

func TestDiagnosticsUseTheVersionTheyName(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	if err := h.session.Open(path, "java", newText(1, "one\ntwo\nthree\n")); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if err := h.session.Change(path, newText(2, "one\ntwo!\nthree\n")); err != nil {
		t.Fatal(err)
	}
	h.next(t)

	type published struct {
		path    string
		version uint64
		ds      []Diagnostic
	}
	got := make(chan published, 4)
	h.session.SetHandlers(func(path string, version uint64, ds []Diagnostic) {
		got <- published{path: path, version: version, ds: ds}
	}, nil)

	report := func(version uint64, hasVersion bool) {
		t.Helper()
		params := map[string]any{
			"uri": fileURI(path),
			"diagnostics": []map[string]any{{
				"range": map[string]any{
					"start": map[string]int{"line": 2, "character": 0},
					"end":   map[string]int{"line": 2, "character": 5},
				},
				"severity": 1,
				"message":  "bad",
				"source":   "java",
				"code":     42,
			}},
		}
		if hasVersion {
			params["version"] = version
		}
		h.remote.Notify("textDocument/publishDiagnostics", params)
	}
	wait := func() published {
		t.Helper()
		select {
		case p := <-got:
			return p
		case <-time.After(2 * time.Second):
			t.Fatal("no diagnostics")
			return published{}
		}
	}

	report(2, true)
	if p := wait(); p.version != 2 || len(p.ds) != 1 || p.ds[0].Start != 9 || p.ds[0].End != 14 ||
		p.ds[0].Severity != 0 || p.ds[0].Message != "bad" || p.ds[0].Source != "java" || p.ds[0].Code != "42" {
		t.Errorf("version 2: %+v", p)
	}
	// The older version is still readable, and its line 2 starts a byte
	// earlier, before the "!" was inserted on the line above.
	report(1, true)
	if p := wait(); p.version != 1 || len(p.ds) != 1 || p.ds[0].Start != 8 || p.ds[0].End != 13 {
		t.Errorf("version 1: %+v", p)
	}
	// Without a version the newest text is used, and the version is unknown.
	report(0, false)
	if p := wait(); p.version != 0 || len(p.ds) != 1 || p.ds[0].Start != 9 || p.ds[0].End != 14 {
		t.Errorf("no version: %+v", p)
	}

	h.remote.Notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         fileURI("/tmp/Other.java"),
		"diagnostics": []map[string]any{},
	})
	select {
	case p := <-got:
		t.Errorf("untracked file reported: %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStatusMapping(t *testing.T) {
	h := newHarness(t, `{}`)
	type update struct {
		status  Status
		message string
	}
	got := make(chan update, 8)
	h.session.SetHandlers(nil, func(status Status, message string) {
		got <- update{status: status, message: message}
	})
	for _, tc := range []struct {
		kind, message string
		want          Status
	}{
		{"Starting", "", StatusStarting},
		{"Started", "", StatusStarting},
		{"Message", "hello", StatusImporting},
		{"ServiceReady", "", StatusReady},
		{"ProjectStatus", "OK", StatusReady},
		{"ProjectStatus", "WARNING", StatusImporting},
		{"Error", "boom", StatusError},
	} {
		h.remote.Notify("language/status", map[string]string{"type": tc.kind, "message": tc.message})
		select {
		case u := <-got:
			if u.status != tc.want || u.message != tc.message {
				t.Errorf("%s: %+v, want %v", tc.kind, u, tc.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: no status", tc.kind)
		}
	}
}

func TestCompleteListWithTextEditRange(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	h.session.Open(path, "java", newText(1, "class A { int x; }\n"))
	h.next(t)
	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/completion" {
			return nil
		}
		return json.RawMessage(`{"items":[{"label":"x","detail":"int x","kind":5,"insertText":"ignored","documentation":{"kind":"markdown","value":"the x"},"filterText":"x","textEdit":{"newText":"x","range":{"start":{"line":0,"character":14},"end":{"line":0,"character":15}}}}]}`)
	})
	start, items, err := h.session.Complete(path, 15)
	if err != nil {
		t.Fatal(err)
	}
	n := h.next(t)
	if n.method != "textDocument/completion" {
		t.Fatalf("method %s", n.method)
	}
	var req struct {
		Position position `json:"position"`
	}
	if err := json.Unmarshal(n.params, &req); err != nil {
		t.Fatal(err)
	}
	if req.Position != (position{Line: 0, Character: 15}) {
		t.Errorf("position %+v", req.Position)
	}
	if start != 14 {
		t.Errorf("start %d", start)
	}
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	it := items[0]
	if it.Label != "x" || it.Detail != "int x" || it.Insert != "x" || it.Documentation != "the x" ||
		it.FilterText != "x" || it.Kind != 5 || it.Snippet {
		t.Errorf("item %+v", it)
	}
}

func TestCompleteListHonoursItemDefaults(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	h.session.Open(path, "java", newText(1, "class A { int x; }\n"))
	h.next(t)
	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/completion" {
			return nil
		}
		return json.RawMessage(`{"items":[{"label":"x"}],"itemDefaults":{"editRange":{"start":{"line":0,"character":10},"end":{"line":0,"character":12}}}}`)
	})
	start, items, err := h.session.Complete(path, 15)
	if err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if start != 10 {
		t.Errorf("start %d, want the itemDefaults range start 10", start)
	}
	if len(items) != 1 || items[0].Label != "x" || items[0].Insert != "" {
		t.Errorf("items %+v", items)
	}
}

func TestCompleteArrayFallsBackToWordStart(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	h.session.Open(path, "java", newText(1, "class A { int x; }\n"))
	h.next(t)
	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/completion" {
			return nil
		}
		return json.RawMessage(`[{"label":"foo","insertText":"bar","insertTextFormat":2}]`)
	})
	start, items, err := h.session.Complete(path, 15)
	if err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if start != 14 {
		t.Errorf("start %d, want the word \"x\" at 14", start)
	}
	if len(items) != 1 || items[0].Insert != "bar" || !items[0].Snippet {
		t.Errorf("items %+v", items)
	}
}

func TestHoverContents(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	path := "/tmp/A.java"
	h.session.Open(path, "java", newText(1, "class A {}\n"))
	h.next(t)

	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/hover" {
			return nil
		}
		return json.RawMessage(`{"contents":{"kind":"markdown","value":"**A**"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}`)
	})
	start, end, body, err := h.session.Hover(path, 7)
	if err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if start != 0 || end != 5 || body != "**A**" {
		t.Errorf("hover %d..%d %q", start, end, body)
	}

	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/hover" {
			return nil
		}
		return json.RawMessage(`{"contents":[{"kind":"plaintext","value":"one"},"two",{"language":"java","value":"three"}]}`)
	})
	start, end, body, err = h.session.Hover(path, 7)
	if err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if start != 7 || end != 7 || body != "one\n\ntwo\n\nthree" {
		t.Errorf("hover %d..%d %q", start, end, body)
	}
}

func TestDefinitionLocationLink(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	dir := t.TempDir()
	path := filepath.Join(dir, "A.java")
	target := filepath.Join(dir, "B.java")
	if err := h.session.Open(path, "java", newText(1, "class A extends B {}\n")); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if err := os.WriteFile(target, []byte("package p;\nclass B {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/definition" {
			return nil
		}
		return []map[string]any{
			{
				"targetUri": fileURI(target),
				"targetRange": map[string]any{
					"start": map[string]int{"line": 1, "character": 6},
					"end":   map[string]int{"line": 1, "character": 7},
				},
			},
			{
				"uri": "jdt://contents/java.base/java.lang/Object.class",
				"range": map[string]any{
					"start": map[string]int{"line": 0, "character": 0},
					"end":   map[string]int{"line": 0, "character": 0},
				},
			},
		}
	})
	locs, err := h.session.Definition(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	h.next(t)
	if len(locs) != 1 {
		t.Fatalf("locations %+v", locs)
	}
	l := locs[0]
	if l.Path != target || l.Start != 17 || l.End != 18 {
		t.Errorf("location %+v", l)
	}
}

func TestBuildFilesChanged(t *testing.T) {
	h := newHarness(t, `{}`)
	if err := h.session.BuildFilesChanged("/p/pom.xml", "/p/build.gradle"); err != nil {
		t.Fatal(err)
	}
	n := h.next(t)
	if n.method != "workspace/didChangeWatchedFiles" {
		t.Fatalf("method %s", n.method)
	}
	var p struct {
		Changes []struct {
			URI  string `json:"uri"`
			Type int    `json:"type"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(n.params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 2 ||
		p.Changes[0].URI != fileURI("/p/pom.xml") || p.Changes[0].Type != 2 ||
		p.Changes[1].URI != fileURI("/p/build.gradle") || p.Changes[1].Type != 2 {
		t.Errorf("changes %+v", p.Changes)
	}
}

func TestCallsBeforeAttachFail(t *testing.T) {
	s := New()
	if err := s.Open("/tmp/A.java", "java", newText(1, "")); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Open: %v", err)
	}
	if _, _, _, err := s.Hover("/tmp/A.java", 0); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Hover: %v", err)
	}
	if err := s.BuildFilesChanged("/p/pom.xml"); !errors.Is(err, ErrNotStarted) {
		t.Errorf("BuildFilesChanged: %v", err)
	}
}

func TestAnswersServerRequests(t *testing.T) {
	h := newHarness(t, `{}`)
	var config []any
	if err := h.remote.Call("workspace/configuration", map[string]any{
		"items": []map[string]any{{"section": "java"}, {"section": "other"}},
	}, &config); err != nil {
		t.Fatal(err)
	}
	if len(config) != 2 || config[0] != nil || config[1] != nil {
		t.Errorf("configuration %#v", config)
	}
	var edit struct {
		Applied       bool   `json:"applied"`
		FailureReason string `json:"failureReason"`
	}
	if err := h.remote.Call("workspace/applyEdit", map[string]any{}, &edit); err != nil {
		t.Fatal(err)
	}
	if edit.Applied || edit.FailureReason == "" {
		t.Errorf("applyEdit %+v", edit)
	}
	var ignored any
	if err := h.remote.Call("client/registerCapability", map[string]any{}, &ignored); err != nil {
		t.Fatal(err)
	}
}

func TestImplementationIsKeptUntilTheCodeChanges(t *testing.T) {
	h := newHarness(t, `{"textDocumentSync":{"change":2}}`)
	dir := t.TempDir()
	path := filepath.Join(dir, "A.java")
	target := filepath.Join(dir, "B.java")
	os.WriteFile(target, []byte("class B {}\n"), 0o644)
	if err := h.session.Open(path, "java", newText(1, "class A {}\n")); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	var asked atomic.Int32
	h.fake.respond(func(method string, params json.RawMessage) any {
		if method != "textDocument/implementation" {
			return nil
		}
		asked.Add(1)
		return []map[string]any{{
			"uri": fileURI(target),
			"range": map[string]any{
				"start": map[string]int{"line": 0, "character": 6},
				"end":   map[string]int{"line": 0, "character": 7},
			},
		}}
	})
	ask := func(want int32, why string) {
		t.Helper()
		locs, err := h.session.Implementation(path, 6)
		if err != nil || len(locs) != 1 || locs[0].Path != target {
			t.Fatalf("%s: %+v, %v", why, locs, err)
		}
		if got := asked.Load(); got != want {
			t.Fatalf("%s: server asked %d times, want %d", why, got, want)
		}
	}
	ask(1, "first")
	ask(1, "again")
	// A change anywhere may add an implementation.
	if err := h.session.Change(path, newText(2, "class A extends X {}\n")); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	ask(2, "after a change")
	ask(2, "kept again")
	if err := h.session.Save(path); err != nil {
		t.Fatal(err)
	}
	h.next(t)
	ask(3, "after a save")
}
