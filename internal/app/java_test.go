package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"megafactory/internal/javalang"
	"megafactory/internal/setup"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// fakeJava is the language server tests give instead of jdtls: it
// records what the app sends, and does what a test tells it.
type fakeJava struct {
	project string
	diag    func(string, uint64, []javalang.Diagnostic)
	status  func(javalang.Status, string)

	opened  map[string]uint64
	changes map[string]uint64
	sent    map[string]uint64
	saves   []string
	closes  []string
	build   [][]string
	stopped int

	// completion, hover and definition answer as a test says; the path
	// and offset asked about are kept for the test to check.
	complete func(path string, offset int) (int, []javalang.Item, error)
	hover    func(path string, offset int) (int, int, string, error)
	define   func(path string, offset int) ([]javalang.Location, error)
	asked    string
	at       int

	mainClasses []javalang.MainClass
	classPaths  [][]string
	resolveErr  error
	resolved    string

	// debugPort is what startDebugSession answers; 0 fails the session.
	debugPort int
}

func (f *fakeJava) StartDebugSession() (int, error) {
	if f.debugPort == 0 {
		return 0, errors.New("no debug port")
	}
	return f.debugPort, nil
}

func newFakeJava() *fakeJava {
	return &fakeJava{
		opened:  map[string]uint64{},
		changes: map[string]uint64{},
		sent:    map[string]uint64{},
	}
}

func (f *fakeJava) Open(path, languageID string, text javalang.Text) error {
	if languageID != "java" {
		return fmt.Errorf("language %q", languageID)
	}
	f.opened[path] = text.Version()
	f.sent[path] = text.Version()
	return nil
}

func (f *fakeJava) Change(path string, text javalang.Text) error {
	f.changes[path] = text.Version()
	f.sent[path] = text.Version()
	return nil
}

func (f *fakeJava) Save(path string) error {
	f.saves = append(f.saves, path)
	return nil
}

func (f *fakeJava) Close(path string) error {
	f.closes = append(f.closes, path)
	return nil
}

func (f *fakeJava) Complete(path string, offset int) (int, []javalang.Item, error) {
	f.asked, f.at = path, offset
	if f.complete == nil {
		return 0, nil, nil
	}
	return f.complete(path, offset)
}

func (f *fakeJava) Hover(path string, offset int) (int, int, string, error) {
	f.asked, f.at = path, offset
	if f.hover == nil {
		return 0, 0, "", nil
	}
	return f.hover(path, offset)
}

func (f *fakeJava) Definition(path string, offset int) ([]javalang.Location, error) {
	f.asked, f.at = path, offset
	if f.define == nil {
		return nil, nil
	}
	return f.define(path, offset)
}

func (f *fakeJava) BuildFilesChanged(paths ...string) error {
	f.build = append(f.build, slices.Clone(paths))
	return nil
}

func (f *fakeJava) ResolveMainClasses() ([]javalang.MainClass, error) {
	return f.mainClasses, f.resolveErr
}

func (f *fakeJava) ResolveClasspath(mainClass, projectName string) ([]string, []string, error) {
	f.resolved = mainClass + "/" + projectName
	if f.resolveErr != nil {
		return nil, nil, f.resolveErr
	}
	var modules, classes []string
	if len(f.classPaths) > 0 {
		modules = f.classPaths[0]
	}
	if len(f.classPaths) > 1 {
		classes = f.classPaths[1]
	}
	return modules, classes, nil
}

func (f *fakeJava) starter(project string, diag func(string, uint64, []javalang.Diagnostic), status func(javalang.Status, string)) (langServer, func(), error) {
	f.project, f.diag, f.status = project, diag, status
	return f, func() { f.stopped++ }, nil
}

// newJavaApp opens the sample with a fake language server whose
// notifications run where they are made, so a frame sends them.
func newJavaApp(t *testing.T, dir string) (*App, *fakeJava, *ui.Tester) {
	t.Helper()
	a := newApp(defaultState)
	a.branch = "dev"
	f := newFakeJava()
	a.java.syncQueue = true
	a.java.starter = f.starter
	a.OpenProject(dir)
	return a, f, ui.NewTester(a.View, 1280, 800)
}

// openDemo opens the sample and its DemoApplication.java.
func openDemo(t *testing.T, dir string) (*App, *fakeJava, *ui.Tester) {
	t.Helper()
	a, f, tt := newJavaApp(t, dir)
	click(t, tt, "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
	return a, f, tt
}

const demoRel = "src/main/java/com/example/demo/DemoApplication.java"

func TestJavaStartsAndOpensDocument(t *testing.T) {
	dir := sampleProject(t)
	a, f, tt := newJavaApp(t, dir)
	if f.project != dir {
		t.Fatalf("starter project %q, want %q", f.project, dir)
	}
	if a.java.srv != f || a.java.status != javaStarting {
		t.Fatalf("server %v, status %d", a.java.srv, a.java.status)
	}
	click(t, tt, "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
	file := filepath.Join(dir, filepath.FromSlash(demoRel))
	if _, ok := f.opened[file]; !ok {
		t.Fatalf("didOpen %v, want %s", f.opened, file)
	}
}

func TestJavaDocumentSync(t *testing.T) {
	dir := sampleProject(t)
	a, f, tt := openDemo(t, dir)
	file := filepath.Join(dir, filepath.FromSlash(demoRel))
	tb := a.tabs[a.active]
	opened := f.opened[file]
	tb.ed.Insert("// note\n")
	tt.Frame()
	if got, want := f.changes[file], tb.ed.Buffer.Version(); got != want || got <= opened {
		t.Errorf("didChange at %d, buffer at %d, opened at %d", got, want, opened)
	}
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()
	if !slices.Contains(f.saves, file) {
		t.Errorf("didSave %v, want %s", f.saves, file)
	}
	click(t, tt, "Close DemoApplication.java")
	if !slices.Contains(f.closes, file) {
		t.Errorf("didClose %v, want %s", f.closes, file)
	}
}

func TestJavaDiagnosticsInProblems(t *testing.T) {
	dir := sampleProject(t)
	a, f, tt := openDemo(t, dir)
	file := filepath.Join(dir, filepath.FromSlash(demoRel))
	tb := a.tabs[a.active]
	snap := tb.ed.Buffer.Snapshot()
	start := snap.LineStart(2)
	f.diag(file, snap.Version(), []javalang.Diagnostic{{Start: start, End: start + 1, Severity: 0, Message: "Type mismatch"}})
	tt.Frame()
	if got := tb.ed.Diagnostics(); len(got) != 1 || got[0].Message != "Type mismatch" {
		t.Fatalf("tab diagnostics %+v", got)
	}
	click(t, tt, "Problems (1)")
	if !tt.HasText("Type mismatch") || !tt.HasText("DemoApplication.java:3") {
		t.Fatalf("problems panel: %v", tt.Texts())
	}
	click(t, tt, "src/main/java/com/example/demo/DemoApplication.java:3")
	tt.Frame()
	if a.tabs[a.active].path != demoRel {
		t.Fatalf("active tab %q", a.tabs[a.active].path)
	}
	if line := a.tabs[a.active].ed.CursorPos().Line; line != 2 {
		t.Errorf("caret on line %d, want 2", line)
	}
}

func TestJavaStatusBar(t *testing.T) {
	dir := sampleProject(t)
	_, f, tt := newJavaApp(t, dir)
	f.status(javalang.StatusReady, "")
	tt.Frame()
	if !tt.HasText("jdtls: ready") {
		t.Fatalf("status bar: %v", tt.Texts())
	}
	f.status(javalang.StatusImporting, "Importing Maven project")
	tt.Frame()
	if !tt.HasText("Importing Maven project") {
		t.Errorf("importing not shown: %v", tt.Texts())
	}
}

func TestJavaEditorFeatures(t *testing.T) {
	dir := sampleProject(t)
	a, f, tt := openDemo(t, dir)
	file := filepath.Join(dir, filepath.FromSlash(demoRel))
	tb := a.tabs[a.active]

	f.complete = func(string, int) (int, []javalang.Item, error) {
		return 0, []javalang.Item{{Label: "println", Kind: 2, Insert: "println"}}, nil
	}
	tb.ed.OnCompletion(editor.CompletionRequest{Offset: 3, Prefix: "pri", Version: tb.ed.Buffer.Version()})
	tt.Frame()
	if f.asked != file || f.at != 3 {
		t.Errorf("completion asked of %q at %d", f.asked, f.at)
	}
	if !tt.HasText("println") {
		t.Fatalf("completion not shown: %v", tt.Texts())
	}

	f.hover = func(string, int) (int, int, string, error) { return 0, 5, "String java.lang.String", nil }
	tb.ed.OnHover(editor.HoverRequest{Offset: 0, Version: tb.ed.Buffer.Version()})
	tt.Frame()
	if !tt.HasText("String java.lang.String") {
		t.Fatalf("hover not shown: %v", tt.Texts())
	}

	helloRel := "src/main/java/com/example/demo/HelloController.java"
	hello := filepath.Join(dir, filepath.FromSlash(helloRel))
	data, err := os.ReadFile(hello)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.Index(data, []byte("public String hello"))
	if offset < 0 {
		t.Fatal("hello() not found")
	}
	line := bytes.Count(data[:offset], []byte("\n"))
	f.define = func(string, int) ([]javalang.Location, error) {
		return []javalang.Location{{Path: hello, Start: offset}}, nil
	}
	tb.ed.OnDefinition(0)
	tt.Frame()
	if a.tabs[a.active].path != helloRel {
		t.Fatalf("definition opened %q, want %q", a.tabs[a.active].path, helloRel)
	}
	if got := a.tabs[a.active].ed.CursorPos().Line; got != line {
		t.Errorf("caret on line %d, want %d", got, line)
	}
}

func TestInlineErrorsSetting(t *testing.T) {
	defer func(d string) { dataDir = d }(dataDir)
	dataDir = t.TempDir()
	defer func() { prefs = defaultSettings }()
	dir := sampleProject(t)
	a, f, tt := openDemo(t, dir)
	file := filepath.Join(dir, filepath.FromSlash(demoRel))
	tb := a.tabs[a.active]
	snap := tb.ed.Buffer.Snapshot()
	start := snap.LineStart(4)
	f.diag(file, snap.Version(), []javalang.Diagnostic{{Start: start, End: start + 1, Severity: 1, Message: "Unused import\nmore"}})
	tt.Frame()
	if n := len(a.java.inlays[demoRel]); n != 1 {
		t.Fatalf("inlays %d, want 1", n)
	}
	// The panel takes the room the Java section would need.
	click(t, tt, "Hide Panel  ⌘J · ⌃`", "Settings  ⌘,", "Show problems inline")
	tt.Frame()
	if prefs.InlineErrors || len(a.java.inlays[demoRel]) != 0 {
		t.Errorf("setting %v, inlays %d", prefs.InlineErrors, len(a.java.inlays[demoRel]))
	}
	if LoadSettings().InlineErrors {
		t.Error("the setting was not kept")
	}
	click(t, tt, "Show problems inline")
	tt.Frame()
	if !prefs.InlineErrors || len(a.java.inlays[demoRel]) != 1 {
		t.Errorf("back on: setting %v, inlays %d", prefs.InlineErrors, len(a.java.inlays[demoRel]))
	}
}

func TestBuildPageTasks(t *testing.T) {
	dir := sampleProject(t)
	// A wrapper, so the command line is the project's own, not PATH's.
	if err := os.WriteFile(filepath.Join(dir, "mvnw"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, _, tt := newJavaApp(t, dir)
	click(t, tt, "Build")
	tt.Frame()
	if !tt.HasText("compile") || !tt.HasText("package") {
		t.Fatalf("no tasks: %v", tt.Texts())
	}
	click(t, tt, "package")
	tt.Frame()
	if a.builds.selectedTask != "package" {
		t.Errorf("selected task %q", a.builds.selectedTask)
	}
	if !tt.HasText("mvnw package") {
		t.Errorf("no command line: %v", tt.Texts())
	}
	// A JDK's download, then its failure, show in the status bar.
	a.java.status, a.java.downloading = javaStarting, true
	a.java.progress = setup.Progress{Name: "jdk", Done: 3 << 20, Total: -1}
	tt.Frame()
	if !tt.HasText("Downloading jdk 3 MB") {
		t.Fatalf("no progress: %v", tt.Texts())
	}
	a.java.err = errors.New("no JDK 25 found")
	a.javaSetStatus(javalang.StatusError, "")
	tt.Frame()
	if !tt.HasText("jdtls: failed") {
		t.Errorf("failure not shown: %v", tt.Texts())
	}
}

func TestBuildPageWithoutBuildSystem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a := newApp(defaultState)
	a.OpenProject(dir)
	tt := ui.NewTester(a.View, 1280, 800)
	click(t, tt, "Build")
	tt.Frame()
	if !tt.HasText("No Maven or Gradle build") {
		t.Fatalf("no explanation: %v", tt.Texts())
	}
}

func TestBuildFilesChangedOffersSync(t *testing.T) {
	dir := sampleProject(t)
	a := newApp(defaultState)
	a.branch = "dev"
	var changed func([]string)
	a.host.Watch = func(_ string, fn func([]string)) func() { changed = fn; return func() {} }
	f := newFakeJava()
	a.java.syncQueue = true
	a.java.starter = f.starter
	a.OpenProject(dir)
	tt := ui.NewTester(a.View, 1280, 800)
	pom := filepath.Join(dir, "pom.xml")
	data, err := os.ReadFile(pom)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pom, append(data, []byte("<!-- edited -->\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	changed([]string{"."})
	tt.Frame()
	if !tt.HasText("Build files changed — Sync") {
		t.Fatalf("no sync offer: %v", tt.Texts())
	}
	click(t, tt, "Build files changed — Sync")
	tt.Frame()
	if len(f.build) != 1 || !slices.Contains(f.build[0], pom) {
		t.Fatalf("BuildFilesChanged %v, want %s", f.build, pom)
	}
	if a.java.buildChanged {
		t.Error("the offer stayed after the sync")
	}
}
