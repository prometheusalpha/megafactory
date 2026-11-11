package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"megafactory/internal/runcfg"

	"github.com/egoist/mygo/ui"
)

func openSample(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a := newApp(defaultState)
	a.branch = "dev" // not the folder main's name, which tests click
	a.OpenProject(sampleProject(t))
	return a, ui.NewTester(a.View, 1280, 800)
}

func click(t *testing.T, tt *ui.Tester, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := tt.Click(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenAndCloseTabs(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "src", "main", "java", "com", "example", "demo", "DemoApplication.java", "pom.xml", "DemoApplication.java")
	if len(a.tabs) != 2 || a.tabs[a.active].name != "DemoApplication.java" {
		t.Fatalf("tabs %d, active %q", len(a.tabs), a.tabs[a.active].name)
	}
	if got := a.tabs[a.active].ed.Buffer.Line(0); got != "package com.example.demo;" {
		t.Errorf("first line %q", got)
	}
	click(t, tt, "Close pom.xml")
	if len(a.tabs) != 1 || a.tabs[a.active].name != "DemoApplication.java" {
		t.Fatalf("after close: tabs %d, active %q", len(a.tabs), a.tabs[a.active].name)
	}
}

func TestTreeLoadsFoldersOnOpen(t *testing.T) {
	a, tt := openSample(t)
	src := a.root.children[0]
	if src.name != "src" || src.loaded || tt.HasText("main") {
		t.Fatalf("src %+v loaded before opening", src)
	}
	click(t, tt, "src")
	if !src.loaded || len(src.children) != 1 || !tt.HasText("main") {
		t.Error("src not read when opened")
	}
}

func TestTreeDimsGitIgnored(t *testing.T) {
	dir := sampleProject(t)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("target/\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "target", "classes"), 0o755)
	a := newApp(defaultState)
	a.OpenProject(dir)
	ignored := map[string]bool{}
	for _, n := range a.root.children {
		ignored[n.name] = n.ignored
	}
	if !ignored[".git"] || !ignored["target"] || ignored["src"] || ignored[".gitignore"] {
		t.Errorf("ignored %v", ignored)
	}
}

func TestOpenFolderReplacesProject(t *testing.T) {
	a := newApp(defaultState)
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText("No folder open") {
		t.Fatal("a first run should show no project")
	}
	dir := sampleProject(t)
	a.host.PickFolder = func(done func(string)) { done(dir) }
	tt.Key(ui.Cmd, ui.KeyO)
	if a.project != dir || !tt.HasText("pom.xml") {
		t.Fatalf("project %q", a.project)
	}
	if st := LoadState(); st.Project != dir {
		t.Errorf("state kept project %q", st.Project)
	}
}

func TestSettingsChangeTheme(t *testing.T) {
	defer func() { prefs, themes = defaultSettings, codeTheme{} }()
	a := newApp(defaultState)
	var source string
	a.host.SetAppearance = func(s string) { source = s }
	tt := ui.NewTester(a.View, 1280, 800)
	click(t, tt, "Settings  ⌘,", "Catppuccin Mocha", "Dark")
	if themes.dark == nil || prefs.Theme != "Catppuccin Mocha" {
		t.Errorf("theme %q not applied", prefs.Theme)
	}
	if source != "dark" || prefs.Appearance != "dark" {
		t.Errorf("appearance %q, source %q", prefs.Appearance, source)
	}
	if got := LoadSettings(); !reflect.DeepEqual(got, prefs) {
		t.Errorf("kept %+v, want %+v", got, prefs)
	}
	click(t, tt, "Default")
	if themes.light != nil {
		t.Error("Default kept a theme")
	}
}

func TestHideBottomPanel(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "Hide Panel  ⌘J")
	if a.showBottom || tt.HasText("Terminal") {
		t.Errorf("panel still shown: %v", a.showBottom)
	}
}

func TestChooseRunConfig(t *testing.T) {
	a, tt := openSample(t)
	cfg, err := a.runStore.Upsert(runcfg.Config{Name: "All tests", Kind: runcfg.Maven, Goals: "test"})
	if err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Run Configuration", "All tests")
	if a.runConfig != cfg.ID {
		t.Errorf("run config %q, want %q", a.runConfig, cfg.ID)
	}
}

func TestTreeFollowsChangesOnDisk(t *testing.T) {
	a := newApp(defaultState)
	a.branch = "dev"
	var changed func([]string)
	a.host.Watch = func(_ string, fn func([]string)) func() { changed = fn; return func() {} }
	dir := sampleProject(t)
	a.OpenProject(dir)
	tt := ui.NewTester(a.View, 1280, 800)
	click(t, tt, "src", "main")
	main := a.root.at("src/main")

	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	os.Remove(filepath.Join(dir, "pom.xml"))
	os.MkdirAll(filepath.Join(dir, "src/main/kotlin"), 0o755)
	// The watch hints at the root alone: what changed under it is read too.
	changed([]string{"."})
	tt.Frame()
	if !tt.HasText("notes.txt") || tt.HasText("pom.xml") || !tt.HasText("kotlin") {
		t.Error("tree did not follow the disk")
	}
	if a.root.at("src/main") != main || !main.open {
		t.Error("an open folder closed when read again")
	}
	// A hint at a folder the tree hasn't listed reads its nearest listed one.
	os.MkdirAll(filepath.Join(dir, "src/test/java"), 0o755)
	changed([]string{"src/test/java"})
	tt.Frame()
	if !tt.HasText("test") {
		t.Error("new folder src/test not listed")
	}
}

func TestOpenProjectStopsOldWatch(t *testing.T) {
	a := newApp(defaultState)
	stopped := 0
	var changes []func([]string)
	a.host.Watch = func(_ string, fn func([]string)) func() {
		changes = append(changes, fn)
		return func() { stopped++ }
	}
	old := sampleProject(t)
	a.OpenProject(old)
	a.OpenProject(sampleProject(t))
	if stopped != 1 {
		t.Errorf("stopped %d watches, want 1", stopped)
	}
	// A late batch of the old watch leaves the new tree alone.
	os.WriteFile(filepath.Join(old, "late.txt"), nil, 0o644)
	changes[0]([]string{"."})
	if a.root.at("late.txt") != a.root {
		t.Error("old project's change reached the new tree")
	}
}

// sampleWithMenu opens the sample with its src/main folder open, a host
// whose Trash deletes, and whose errors fail the test.
func sampleWithMenu(t *testing.T) (*App, *ui.Tester, string) {
	t.Helper()
	a := newApp(defaultState)
	a.branch = "dev"
	a.host.Trash = os.RemoveAll
	a.host.ShowError = func(title, msg string) { t.Errorf("%s: %s", title, msg) }
	dir := sampleProject(t)
	a.OpenProject(dir)
	tt := ui.NewTester(a.View, 1280, 800)
	click(t, tt, "src", "main")
	return a, tt, dir
}

func menu(t *testing.T, tt *ui.Tester, row string, item string) {
	t.Helper()
	rightClick(t, tt, row)
	if err := tt.ChooseMenuItem(item); err != nil {
		t.Fatalf("%s on %s: %v (menu %q)", item, row, err, tt.Menu())
	}
}

// rightClick right-clicks a row of the tree. The project's row is found
// as the one above src: the title bar's chip shows its name first.
func rightClick(t *testing.T, tt *ui.Tester, row string) {
	t.Helper()
	if row != "spring-demo" {
		if err := tt.RightClick(row); err != nil {
			t.Fatal(err)
		}
		return
	}
	r, ok := tt.Find("src")
	if !ok {
		t.Fatal("no src row")
	}
	tt.RightClickAt(r.X+r.W/2, r.Y+r.H/2-26)
}

func onDisk(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func TestTreeNewFileOpensIt(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	menu(t, tt, "resources", "New File…")
	tt.Type("templates/index.html")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !onDisk(dir, "src/main/resources/templates/index.html") {
		t.Fatal("file not created")
	}
	if len(a.tabs) != 1 || a.tabs[0].path != "src/main/resources/templates/index.html" || a.edit != nil {
		t.Errorf("tabs %d, edit %v", len(a.tabs), a.edit)
	}
	// On a file, New Folder makes it beside the file.
	menu(t, tt, "application.properties", "New Folder…")
	tt.Type("static")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !onDisk(dir, "src/main/resources/static") || !tt.HasText("static") {
		t.Error("folder not created beside the file")
	}
}

func TestTreeRenameFollowsTabs(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources", "config.json")
	menu(t, tt, "resources", "Rename…")
	// The whole name is selected: typing replaces it.
	tt.Type("res")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !onDisk(dir, "src/main/res/config.json") || onDisk(dir, "src/main/resources") {
		t.Fatal("folder not renamed")
	}
	if a.tabs[0].path != "src/main/res/config.json" || a.selected != "src/main/res" {
		t.Errorf("tab %q, selected %q", a.tabs[0].path, a.selected)
	}
	// Escape keeps the name.
	menu(t, tt, "res", "Rename…")
	tt.Type("x")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.edit != nil || !onDisk(dir, "src/main/res") {
		t.Error("escape renamed")
	}
}

func TestTreeRenameSelectsStem(t *testing.T) {
	_, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources")
	menu(t, tt, "config.json", "Rename…")
	tt.Type("settings")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !onDisk(dir, "src/main/resources/settings.json") {
		t.Error("the extension was not kept")
	}
}

func TestTreeCopyCutPasteDuplicate(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources")
	menu(t, tt, "config.json", "Duplicate")
	if !onDisk(dir, "src/main/resources/config copy.json") || a.selected != "src/main/resources/config copy.json" {
		t.Fatalf("duplicate: selected %q", a.selected)
	}
	menu(t, tt, "config copy.json", "Cut")
	menu(t, tt, "spring-demo", "Paste")
	if !onDisk(dir, "config copy.json") || onDisk(dir, "src/main/resources/config copy.json") || a.clip != nil {
		t.Fatal("cut and paste did not move it")
	}
	menu(t, tt, "pom.xml", "Copy")
	menu(t, tt, "java", "Paste")
	menu(t, tt, "java", "Paste")
	if !onDisk(dir, "pom.xml") || !onDisk(dir, "src/main/java/pom.xml") || !onDisk(dir, "src/main/java/pom copy.xml") {
		t.Error("copy and paste twice")
	}
}

func TestTreeDeleteClosesTabs(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources", "config.json")
	asked := ""
	a.host.Ask = func(q Question, done func(int)) { asked = q.Message; done(0) }
	menu(t, tt, "resources", "Delete…")
	if onDisk(dir, "src/main/resources") || len(a.tabs) != 0 || tt.HasText("config.json") {
		t.Errorf("deleted %v, tabs %d", !onDisk(dir, "src/main/resources"), len(a.tabs))
	}
	if asked != "Move the folder “resources” to the Trash?" {
		t.Errorf("asked %q", asked)
	}
	// Declined, nothing goes.
	a.host.Ask = func(_ Question, done func(int)) { done(1) }
	menu(t, tt, "java", "Delete…")
	if !onDisk(dir, "src/main/java") {
		t.Error("deleted without a yes")
	}
}

func TestTreeMenuOnRoot(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	var revealed string
	a.host.Reveal = func(p string) { revealed = p }
	rightClick(t, tt, "spring-demo")
	for _, item := range []string{"Rename…", "Delete…", "Cut", "Copy", "Duplicate"} {
		if err := tt.ChooseMenuItem(item); err == nil {
			t.Errorf("%s enabled on the project", item)
			rightClick(t, tt, "spring-demo")
		}
	}
	tt.CloseMenu()
	menu(t, tt, "spring-demo", "Reveal in Finder")
	if revealed != dir {
		t.Errorf("revealed %q", revealed)
	}
	menu(t, tt, "pom.xml", "Copy Path")
	if got := tt.Clipboard(); got != filepath.Join(dir, "pom.xml") {
		t.Errorf("copied path %q", got)
	}
}

func TestBadNames(t *testing.T) {
	for _, c := range []struct {
		name   string
		rename bool
		bad    bool
	}{
		{"App.java", true, false}, {"a/b.txt", false, false}, {"a/b.txt", true, true},
		{"..", false, true}, {"a//b", false, true}, {"a/", false, true},
	} {
		if got := badName(c.name, c.rename) != ""; got != c.bad {
			t.Errorf("badName(%q, %v) = %v", c.name, c.rename, got)
		}
	}
}

func TestTreeShortcuts(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources")
	tt.Key(ui.Cmd, ui.KeyC)
	click(t, tt, "java")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if !onDisk(dir, "src/main/java/resources/config.json") {
		t.Fatalf("⌘C ⌘V: clip %+v, selected %q", a.clip, a.selected)
	}
	// Enter renames, rather than opens, the row with the focus: src
	// closes as it is clicked, and below it is README.md.
	click(t, tt, "src")
	tt.Key(0, ui.KeyDown)
	tabs := len(a.tabs)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.edit == nil || a.edit.node.name != "README.md" || len(a.tabs) != tabs {
		t.Fatalf("enter: edit %+v, tabs %d", a.edit, len(a.tabs))
	}
	// While a name is typed, ⌘C is the input's.
	clip := a.clip
	tt.Key(ui.Cmd, ui.KeyC)
	if a.clip != clip {
		t.Error("⌘C in the input copied the file")
	}
	tt.Type("NOTES")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !onDisk(dir, "NOTES.md") {
		t.Fatal("enter did not rename")
	}

	click(t, tt, "src", "main")
	tt.Key(ui.Cmd, ui.KeyBackspace)
	tt.Frame()
	if onDisk(dir, "src/main") {
		t.Error("⌘⌫ did not delete")
	}
	// The project's row neither renames nor goes.
	rightClick(t, tt, "spring-demo")
	tt.CloseMenu()
	tt.Key(ui.Cmd, ui.KeyBackspace)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !onDisk(dir, "") || a.edit != nil {
		t.Error("the project's row took ⌘⌫ or Enter")
	}
}

// editSample opens config.json of the sample and types into it.
func editSample(t *testing.T) (*App, *ui.Tester, string, *tab) {
	t.Helper()
	a, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources", "config.json")
	tt.Type("// ")
	tt.Frame()
	tb := a.tabs[a.active]
	if !tb.ed.Buffer.Modified() || !tt.HasText("● config.json") {
		t.Fatal("typing did not modify the tab")
	}
	return a, tt, dir, tb
}

func readSample(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSaveWritesTheFile(t *testing.T) {
	_, tt, dir, tb := editSample(t)
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()
	if got := readSample(t, dir, "src/main/resources/config.json"); got != "// {\n  \"feature\": true,\n  \"limit\": 10\n}\n" {
		t.Errorf("saved %q", got)
	}
	if tb.ed.Buffer.Modified() || tt.HasText("● config.json") {
		t.Error("still modified after saving")
	}
}

func TestCloseModifiedTabAsks(t *testing.T) {
	path := "src/main/resources/config.json"
	for _, c := range []struct {
		button       int
		closed, kept bool // the tab closed; the edit on disk
	}{{2, false, false}, {1, true, false}, {0, true, true}} {
		a, tt, dir, _ := editSample(t)
		var asked string
		a.host.Ask = func(q Question, done func(int)) { asked = q.Message; done(c.button) }
		tt.Key(ui.Cmd, ui.KeyW)
		tt.Frame()
		if asked != "Save the changes to “config.json”?" {
			t.Errorf("asked %q", asked)
		}
		if closed := len(a.tabs) == 0; closed != c.closed {
			t.Errorf("button %d: closed %v", c.button, closed)
		}
		if kept := strings.HasPrefix(readSample(t, dir, path), "// "); kept != c.kept {
			t.Errorf("button %d: saved %v", c.button, kept)
		}
	}
}

func TestCloseButtonAsksToo(t *testing.T) {
	a, tt, _, _ := editSample(t)
	asked := false
	a.host.Ask = func(_ Question, done func(int)) { asked = true; done(2) }
	click(t, tt, "Close config.json")
	if !asked || len(a.tabs) != 1 {
		t.Errorf("asked %v, tabs %d", asked, len(a.tabs))
	}
	// Unmodified, ⌘W closes without asking, then the window.
	asked = false
	click(t, tt, "pom.xml")
	tt.Key(ui.Cmd, ui.KeyW)
	closed := false
	a.host.CloseWindow = func() { closed = true }
	if asked || len(a.tabs) != 1 {
		t.Errorf("unmodified: asked %v, tabs %d", asked, len(a.tabs))
	}
	a.tabs[0].ed.Buffer.MarkSaved()
	tt.Key(ui.Cmd, ui.KeyW)
	tt.Key(ui.Cmd, ui.KeyW)
	if len(a.tabs) != 0 || !closed {
		t.Errorf("tabs %d, window closed %v", len(a.tabs), closed)
	}
}

func TestCloseWindowSavesAll(t *testing.T) {
	a, tt, dir, _ := editSample(t)
	click(t, tt, "pom.xml")
	tt.Type("<!-- -->")
	var asked string
	a.host.Ask = func(q Question, done func(int)) { asked = q.Message; done(0) }
	proceeded := false
	if !a.BeforeCloseWindow(func() { proceeded = true }) {
		t.Fatal("the window closed over 2 modified files")
	}
	if asked != "Save the changes to 2 files?" || !proceeded {
		t.Errorf("asked %q, proceeded %v", asked, proceeded)
	}
	if !strings.HasPrefix(readSample(t, dir, "pom.xml"), "<!-- -->") || !strings.HasPrefix(readSample(t, dir, "src/main/resources/config.json"), "// ") {
		t.Error("not all saved")
	}
	if a.BeforeCloseWindow(func() {}) {
		t.Error("asked again with nothing modified")
	}
}

func TestTabsFollowTheDisk(t *testing.T) {
	a := newApp(defaultState)
	a.branch = "dev"
	var changed func([]string)
	a.host.Watch = func(_ string, fn func([]string)) func() { changed = fn; return func() {} }
	dir := sampleProject(t)
	a.OpenProject(dir)
	tt := ui.NewTester(a.View, 1280, 800)
	click(t, tt, "README.md", "pom.xml")
	tt.Type("x")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# new\r\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>\n"), 0o644)
	changed([]string{"."})
	tt.Frame()
	readme, pom := a.tabs[0].ed.Buffer, a.tabs[1].ed.Buffer
	if readme.String() != "# new\n" || readme.Modified() || readme.LineEnding() != "\r\n" {
		t.Errorf("README.md %q, modified %v", readme.String(), readme.Modified())
	}
	if !strings.HasPrefix(pom.String(), "x<?xml") {
		t.Errorf("an edited tab took the disk's text: %q", pom.String()[:10])
	}
}

// tabMenu chooses an item of a tab's context menu, right-clicking its
// close button: the tree shows the tab's name first.
func tabMenu(t *testing.T, tt *ui.Tester, name, item string) {
	t.Helper()
	menu(t, tt, "Close "+name, item)
}

// tabNames are the names of the open tabs, in order.
func tabNames(a *App) []string {
	var names []string
	for _, t := range a.tabs {
		names = append(names, t.name)
	}
	return names
}

func TestTabMenuCloses(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "README.md", "pom.xml", "Settings  ⌘,")
	if got := strings.Join(tabNames(a), ","); got != "README.md,pom.xml,Settings" {
		t.Fatalf("tabs %s", got)
	}
	tabMenu(t, tt, "README.md", "Close to the Right")
	if got := strings.Join(tabNames(a), ","); got != "README.md" {
		t.Errorf("close to the right: %s", got)
	}
	click(t, tt, "pom.xml")
	click(t, tt, "Settings  ⌘,")
	tabMenu(t, tt, "pom.xml", "Close Others")
	if got := strings.Join(tabNames(a), ","); got != "pom.xml" {
		t.Errorf("close others: %s", got)
	}
	// One question for all the modified tabs that close.
	click(t, tt, "README.md")
	tt.Type("x")
	click(t, tt, "pom.xml")
	tt.Type("x")
	var asked []string
	a.host.Ask = func(q Question, done func(int)) { asked = append(asked, q.Message); done(2) }
	tabMenu(t, tt, "README.md", "Close All")
	if len(asked) != 1 || asked[0] != "Save the changes to 2 files?" || len(a.tabs) != 2 {
		t.Errorf("asked %q, tabs %d", asked, len(a.tabs))
	}
	a.host.Ask = func(_ Question, done func(int)) { done(1) }
	tabMenu(t, tt, "README.md", "Close All")
	if len(a.tabs) != 0 {
		t.Errorf("close all, don't save: tabs %d", len(a.tabs))
	}
}

func TestTabMenuRevealAndCopyPath(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	click(t, tt, "resources", "config.json")
	// Fold the tree, and hide it.
	click(t, tt, "src")
	a.showSidebar = false
	tt.Frame()
	tabMenu(t, tt, "config.json", "Reveal in Tree")
	for range 4 {
		tt.Frame()
	}
	if !a.showSidebar || a.selected != "src/main/resources/config.json" || a.revealing != "" || !tt.HasText("application.properties") {
		t.Errorf("sidebar %v, selected %q, revealing %q", a.showSidebar, a.selected, a.revealing)
	}
	tabMenu(t, tt, "config.json", "Copy Path")
	if got := tt.Clipboard(); got != filepath.Join(dir, "src/main/resources/config.json") {
		t.Errorf("copied %q", got)
	}
	// The settings tab has no file.
	click(t, tt, "Settings  ⌘,")
	rightClick(t, tt, "Close Settings")
	if err := tt.ChooseMenuItem("Reveal in Tree"); err == nil {
		t.Error("revealed the settings")
	}
}

// The tree's scroll stays where the user puts it: nothing to reveal is
// not the project's row, whose path is "" too.
func TestTreeScrolls(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "big")
	os.MkdirAll(dir, 0o755)
	for i := range 80 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d.txt", i)), nil, 0o644)
	}
	a := newApp(defaultState)
	a.OpenProject(dir)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Scroll(100, 300, 0, 600)
	tt.Frame()
	tt.Frame()
	if r, _ := tt.Find("file05.txt"); r.H != 0 {
		t.Errorf("file05.txt still at %v after scrolling down", r)
	}
}

func TestSelectOpenedFile(t *testing.T) {
	a, tt, _ := sampleWithMenu(t)
	click(t, tt, "resources", "config.json")
	click(t, tt, "src") // folds the file away
	click(t, tt, "Select Opened File")
	for range 4 {
		tt.Frame()
	}
	if a.selected != "src/main/resources/config.json" || !tt.HasText("config.json") {
		t.Fatalf("selected %q", a.selected)
	}
	// The row has the focus: the tree's keys act on the file.
	tt.Key(ui.Cmd, ui.KeyC)
	if a.clip == nil || a.clip.path != "src/main/resources/config.json" {
		t.Errorf("⌘C after selecting: clip %+v", a.clip)
	}
	// Nothing to select without a file: no button.
	a.tabs = nil
	tt.Frame()
	if _, ok := tt.Find("Select Opened File"); ok {
		t.Error("button shown with no file open")
	}
}

func TestTreeCompactsMiddleFolders(t *testing.T) {
	_, tt := openSample(t)
	prefs.CompactFolders = true
	defer func() { prefs = defaultSettings }()
	tt.Frame() // src is read by the first, and joins main in the next
	// main holds java and resources, which ends the chain.
	click(t, tt, "src/main", "java/com/example/demo")
	if !tt.HasText("DemoApplication.java") || !tt.HasText("java/com/example/demo") {
		t.Error("java/com/example/demo not one row")
	}
	prefs.CompactFolders = false
	tt.Frame()
	if tt.HasText("java/com/example/demo") {
		t.Error("folders still compacted when the setting is off")
	}
}

// A read that answers later must not make the reveal think a folder gone.
func TestRevealWaitsForReads(t *testing.T) {
	a := newApp(defaultState)
	var jobs []func()
	a.host.Run = func(job func() func()) { apply := job(); jobs = append(jobs, apply) }
	a.OpenProject(sampleProject(t))
	tt := ui.NewTester(a.View, 1280, 800)
	rel := "src/main/java/com/example/demo/DemoApplication.java"
	a.revealInTree(rel)
	for range 40 {
		tt.Frame() // frames that fall between a read and its answer
		if len(jobs) == 0 {
			break
		}
		jobs[0]()
		jobs = jobs[1:]
	}
	tt.Frame()
	if a.revealing != "" || !tt.HasText("DemoApplication.java") {
		t.Errorf("revealing %q, file shown %v", a.revealing, tt.HasText("DemoApplication.java"))
	}
}

func TestTreeFollowsActiveTab(t *testing.T) {
	a, tt, _ := sampleWithMenu(t)
	click(t, tt, "resources", "config.json")
	click(t, tt, "application.properties")
	cfg, props := "src/main/resources/config.json", "src/main/resources/application.properties"
	if a.selected != props {
		t.Fatalf("selected %q after opening by click", a.selected)
	}
	// Fold the tree, then go back to the other tab: the tree opens up to it
	// and the keyboard stays in the editor.
	click(t, tt, "src")
	a.activate(cfg)
	for range 4 {
		tt.Frame()
	}
	if a.selected != cfg || a.revealing != "" || a.focus != focusEditor || !tt.HasText("application.properties") {
		t.Errorf("selected %q, revealing %q, focus %v", a.selected, a.revealing, a.focus)
	}
	// A hidden sidebar stays hidden, and shows the file when it comes back.
	click(t, tt, "src")
	a.showSidebar = false
	a.activate(props)
	for range 2 {
		tt.Frame()
	}
	if a.showSidebar || a.selected != props || a.revealing != props {
		t.Errorf("sidebar %v, selected %q, revealing %q", a.showSidebar, a.selected, a.revealing)
	}
	// The settings tab is no file: the selection stays.
	a.openSettings()
	tt.Frame()
	if a.selected != props {
		t.Errorf("selected %q on the settings tab", a.selected)
	}
}
