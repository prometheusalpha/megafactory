package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"megafactory/internal/javalang"
	"megafactory/internal/jdk"
	"megafactory/internal/jdtls"
	"megafactory/internal/picker"
	"megafactory/internal/setup"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// How far the language server is; the status bar says which.
const (
	javaOff = iota
	javaStarting
	javaImporting
	javaReady
	javaFailed
)

// langServer is what the app asks of a running language server: javalang's
// Session has these, and a test gives its own instead.
type langServer interface {
	Open(path, languageID string, text javalang.Text) error
	Change(path string, text javalang.Text) error
	Save(path string) error
	Close(path string) error
	Complete(path string, offset int) (int, []javalang.Item, error)
	Hover(path string, offset int) (int, int, string, error)
	Definition(path string, offset int) ([]javalang.Location, error)
	Implementation(path string, offset int) ([]javalang.Location, error)
	References(path string, offset int) ([]javalang.Location, error)
	BuildFilesChanged(paths ...string) error
	ResolveMainClasses() ([]javalang.MainClass, error)
	ResolveClasspath(mainClass, projectName string) (modules, classes []string, err error)
	// StartDebugSession is java-debug's adapter, on a loopback port the
	// debug client connects to.
	StartDebugSession() (int, error)
}

// javaStarter starts a project's language server: it is given where the
// server publishes diagnostics and status, and returns the server and how
// to stop it, which runs off the UI thread.
type javaStarter func(project string, diag func(path string, version uint64, ds []javalang.Diagnostic), status func(javalang.Status, string)) (srv langServer, stop func(), err error)

// sentDoc is a document as the server last saw it: the version, and the
// text its diagnostics' offsets count in.
type sentDoc struct {
	version uint64
	snap    editor.Snapshot
}

// javaFile is what the server said of one file, to apply when a tab of it
// opens, and where the Problems panel shows it.
type javaFile struct {
	version  uint64
	ds       []javalang.Diagnostic
	rel      string
	problems []javaProblem
}

// javaProblem is a diagnostic as the Problems panel lists it: its last
// known place, in the text its offsets count in.
type javaProblem struct {
	rel      string
	start    int
	severity editor.Severity
	message  string
	line     int // 0-based
}

// javaService is the language server's state, owned by the UI thread.
type javaService struct {
	status      int
	message     string
	err         error
	progress    setup.Progress // a server's or JDK's download
	downloading bool

	// starter is main's; newApp leaves it nil, so tests start no server
	// unless they give a fake one.
	starter javaStarter
	srv     langServer
	stop    func()
	// ops sends document notifications in the order they were made,
	// off the UI thread.
	ops *docQueue

	// project is the project the server is of, or was last started for;
	// gen names the session, so a stopped one's callbacks are dropped.
	project string
	gen     int

	diagnostics map[string]javaFile
	sent        map[string]sentDoc

	// buildChanged says a build file changed since the last import,
	// which the status bar offers to sync; buildSigs is how they were
	// then.
	buildChanged bool
	buildSigs    map[string]fileSig

	// syncQueue sends document notifications where they are made, as
	// tests need: a window must send them off its UI thread.
	syncQueue bool

	// inlays keeps the Java hints each tab shows so they can be merged
	// with Git's.
	inlays map[string][]editor.Inlay
}

func newJavaService() javaService {
	return javaService{
		diagnostics: map[string]javaFile{},
		sent:        map[string]sentDoc{},
		inlays:      map[string][]editor.Inlay{},
	}
}

// docQueue runs document notifications one at a time, in order, on a
// goroutine of its own: the UI thread never waits for the server, and a
// change cannot overtake the open it belongs to. It holds no bound: a
// flood of edits costs memory, not lost notifications.
type docQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	q      []func()
	closed bool
}

func newDocQueue() *docQueue {
	q := &docQueue{}
	q.cond = sync.NewCond(&q.mu)
	go q.run()
	return q
}

// do queues fn, unless the queue is ending.
func (q *docQueue) do(fn func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.q = append(q.q, fn)
	q.cond.Signal()
}

// finish queues fn after the work queued before it, and ends the queue
// once it has run: what a stopped server needs, after its last changes.
func (q *docQueue) finish(fn func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.q = append(q.q, fn)
	q.cond.Signal()
}

func (q *docQueue) run() {
	for {
		q.mu.Lock()
		for len(q.q) == 0 {
			if q.closed {
				q.mu.Unlock()
				return
			}
			q.cond.Wait()
		}
		fn := q.q[0]
		q.q = q.q[1:]
		q.mu.Unlock()
		fn()
	}
}

// isJavaPath reports whether a path is one jdtls keeps: a Java source.
func isJavaPath(rel string) bool { return path.Ext(rel) == ".java" }

// samePath reports whether two paths name the same file, as the server's
// absolute one and the project tree's.
func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

// relative is a path inside the project, with slashes, and whether it is
// in it at all.
func (a *App) relative(file string) (string, bool) {
	root := filepath.Clean(a.project)
	if root == "" || root == "." {
		return "", false
	}
	rel, err := filepath.Rel(root, filepath.Clean(file))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// startJava starts the language server of a project, replacing any
// other's. A project without Java leaves it off.
func (a *App) startJava(project string) {
	a.stopJava()
	j := &a.java
	j.gen++
	gen := j.gen
	j.project = project
	j.status, j.message, j.err = javaOff, "", nil
	j.progress, j.downloading = setup.Progress{}, false
	j.buildChanged = false
	j.buildSigs = buildSignatures(project)
	if j.starter == nil {
		// With no server to start, as in tests, the bundles still
		// install, which the status bar shows.
		a.ensureServers()
		return
	}
	j.status = javaStarting
	diag := func(file string, version uint64, ds []javalang.Diagnostic) {
		a.host.Update(func() {
			if j.gen == gen {
				a.javaDiagnostics(file, version, ds)
			}
		})
	}
	status := func(st javalang.Status, message string) {
		a.host.Update(func() {
			if j.gen == gen {
				a.javaSetStatus(st, message)
			}
		})
	}
	starter := j.starter
	a.host.Run(func() func() {
		srv, stop, err := starter(project, diag, status)
		return func() {
			if j.gen != gen {
				// Stopped while it started: close what it made, off the
				// UI thread, as its stop asks.
				if stop != nil {
					a.host.Run(func() func() { stop(); return func() {} })
				}
				return
			}
			if err != nil {
				j.status, j.err = javaFailed, err
				return
			}
			j.srv, j.stop = srv, stop
			if !j.syncQueue {
				j.ops = newDocQueue()
			}
			a.javaOpenTabs()
		}
	})
}

// stopJava closes the server off the UI thread, after the documents it
// holds, and forgets everything it said: the next project starts anew.
func (a *App) stopJava() {
	j := &a.java
	j.gen++
	srv, stop, ops := j.srv, j.stop, j.ops
	j.srv, j.stop, j.ops = nil, nil, nil
	j.status, j.message, j.err = javaOff, "", nil
	j.progress, j.downloading = setup.Progress{}, false
	j.diagnostics = map[string]javaFile{}
	j.sent = map[string]sentDoc{}
	j.inlays = map[string][]editor.Inlay{}
	j.buildChanged = false
	j.buildSigs = nil
	// The documents the server holds, named with the project they are in,
	// which is still the old one as the caller changes it about.
	var paths []string
	if srv != nil {
		for _, t := range a.tabs {
			if t.ed != nil && isJavaPath(t.path) {
				paths = append(paths, a.abs(t.path))
			}
		}
	}
	closeAll := func() {
		for _, p := range paths {
			srv.Close(p)
		}
		if stop != nil {
			stop()
		}
	}
	switch {
	case ops != nil:
		// After the notifications queued before, so the closes follow the
		// opens the same server saw.
		ops.finish(closeAll)
	case srv != nil:
		a.host.Run(func() func() { closeAll(); return func() {} })
	}
}

// javaOpenTabs opens the Java tabs a server just started knows nothing of.
func (a *App) javaOpenTabs() {
	for _, t := range a.tabs {
		if t.ed != nil && isJavaPath(t.path) {
			a.javaDidOpen(t)
		}
	}
}

// javaTabOpened opens a new tab's document, with what the server already
// said of its file, which a tab opened after its diagnostics arrived needs.
func (a *App) javaTabOpened(t *tab) {
	if t == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	a.javaDidOpen(t)
	if f, ok := a.java.diagnostics[a.abs(t.path)]; ok {
		version := f.version
		snap := t.ed.Buffer.Snapshot()
		if version == 0 {
			// Published without a version: the offsets are the text's now.
			version = snap.Version()
		}
		t.ed.SetDiagnostics(version, javaEditorDiagnostics(f.ds, versionClip(version, t)))
		moved := t.ed.Diagnostics()
		if len(moved) == len(f.ds) {
			f.problems = javaProblems(f.rel, moved, snap)
			a.java.diagnostics[a.abs(t.path)] = f
		}
	}
	a.setInlays(t)
}

// javaDidOpen sends a tab's text whole, unless the server has it already.
func (a *App) javaDidOpen(t *tab) {
	j := &a.java
	if j.srv == nil || t == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	file := a.abs(t.path)
	if _, ok := j.sent[file]; ok {
		// A document a new server needs is one stopJava forgot; an open
		// one changes, it does not open again.
		a.javaChange(t)
		return
	}
	snap := t.ed.Buffer.Snapshot()
	j.sent[file] = sentDoc{version: snap.Version(), snap: snap}
	a.javaRun(j.srv, func(srv langServer) {
		srv.Open(file, "java", snap)
	})
}

// javaChange sends a tab's text when it differs from the server's, and
// opens the document when the server has never seen it.
func (a *App) javaChange(t *tab) {
	j := &a.java
	if j.srv == nil || t == nil || t.ed == nil || !isJavaPath(t.path) || !slices.Contains(a.tabs, t) {
		return
	}
	file := a.abs(t.path)
	snap := t.ed.Buffer.Snapshot()
	last, seen := j.sent[file]
	if !seen {
		a.javaDidOpen(t)
		return
	}
	if last.version >= snap.Version() {
		return
	}
	j.sent[file] = sentDoc{version: snap.Version(), snap: snap}
	a.javaRun(j.srv, func(srv langServer) {
		srv.Change(file, snap)
	})
}

// javaDidSave sends a tab's text, which a server needs before the save if
// the frame that would have sent it has not come yet, then the save.
func (a *App) javaDidSave(t *tab) {
	j := &a.java
	if j.srv == nil || t == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	if _, seen := j.sent[a.abs(t.path)]; !seen {
		return
	}
	a.javaChange(t)
	file := a.abs(t.path)
	a.javaRun(j.srv, func(srv langServer) { srv.Save(file) })
}

// javaDidClose closes a tab's document, so that the server drops what it
// kept of it.
func (a *App) javaDidClose(t *tab) {
	j := &a.java
	if t == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	file := a.abs(t.path)
	delete(j.sent, file)
	delete(j.inlays, t.path)
	if j.srv == nil {
		return
	}
	a.javaRun(j.srv, func(srv langServer) { srv.Close(file) })
}

// javaRun sends one notification through the session's queue, or where it
// is made when a test asks for that.
func (a *App) javaRun(srv langServer, fn func(langServer)) {
	j := &a.java
	if j.syncQueue {
		fn(srv)
		return
	}
	if j.ops == nil {
		return
	}
	j.ops.do(func() { fn(srv) })
}

// javaFrame sends the active tab's text, when it changed since the server
// saw it, on every frame.
func (a *App) javaFrame() {
	if len(a.tabs) == 0 {
		return
	}
	a.javaChange(a.tabs[a.active])
}

// javaSetStatus maps the server's status to the status bar.
func (a *App) javaSetStatus(st javalang.Status, message string) {
	j := &a.java
	j.message = message
	switch st {
	case javalang.StatusStarting:
		j.status = javaStarting
	case javalang.StatusImporting:
		j.status = javaImporting
	case javalang.StatusReady:
		j.status = javaReady
	case javalang.StatusError:
		j.status = javaFailed
	}
	if st != javalang.StatusStarting {
		j.progress, j.downloading = setup.Progress{}, false
	}
}

// javaDiagnostics stores what the server said of a file, and shows it in
// its tab: underlined, and in the inline hints when the settings ask.
func (a *App) javaDiagnostics(file string, version uint64, ds []javalang.Diagnostic) {
	j := &a.java
	file = filepath.Clean(file)
	rel, ok := a.relative(file)
	if !ok {
		return
	}
	f := javaFile{version: version, ds: ds, rel: rel}
	shown := false
	for _, t := range a.tabs {
		if t.ed == nil || !samePath(a.abs(t.path), file) {
			continue
		}
		v := version
		if v == 0 {
			// Published without a version: its offsets are the text's now.
			v = t.ed.Buffer.Version()
		}
		snap := t.ed.Buffer.Snapshot()
		t.ed.SetDiagnostics(v, javaEditorDiagnostics(ds, versionClip(v, t)))
		f.problems = javaProblems(rel, t.ed.Diagnostics(), snap)
		shown = true
		a.setInlays(t)
	}
	if !shown {
		// No tab shows it: place it in the text last sent, as the Problems
		// panel and a tab opened after keep it.
		last := j.sent[file]
		f.problems = javaProblemsAt(rel, ds, last.snap, last.version > 0)
	}
	if len(ds) == 0 {
		delete(j.diagnostics, file)
	} else {
		j.diagnostics[file] = f
	}
}

// versionClip is how far diagnostics of v may run in the tab's text: only
// those of its current version are placed in it, and clamped to it.
func versionClip(v uint64, t *tab) int {
	if v == t.ed.Buffer.Version() {
		return t.ed.Buffer.Len()
	}
	return -1
}

// javaEditorDiagnostics maps the server's diagnostics to the editor's.
func javaEditorDiagnostics(ds []javalang.Diagnostic, clip int) []editor.Diagnostic {
	out := make([]editor.Diagnostic, len(ds))
	for i, d := range ds {
		start, end := d.Start, d.End
		if clip >= 0 {
			start = min(max(start, 0), clip)
			end = min(max(end, start), clip)
		}
		out[i] = editor.Diagnostic{
			Start:    start,
			End:      end,
			Severity: editor.Severity(min(max(d.Severity, 0), 3)),
			Message:  d.Message,
			Source:   d.Source,
		}
	}
	return out
}

// javaProblems places diagnostics that count in snap.
func javaProblems(rel string, ds []editor.Diagnostic, snap editor.Snapshot) []javaProblem {
	out := make([]javaProblem, len(ds))
	for i, d := range ds {
		out[i] = javaProblem{
			rel:      rel,
			start:    d.Start,
			severity: d.Severity,
			message:  d.Message,
			line:     lineAt(snap, d.Start),
		}
	}
	return out
}

// javaProblemsAt places raw diagnostics in snap, if it is their version's.
func javaProblemsAt(rel string, ds []javalang.Diagnostic, snap editor.Snapshot, has bool) []javaProblem {
	out := make([]javaProblem, len(ds))
	for i, d := range ds {
		line := 0
		if has {
			line = lineAt(snap, d.Start)
		}
		out[i] = javaProblem{
			rel:      rel,
			start:    d.Start,
			severity: editor.Severity(min(max(d.Severity, 0), 3)),
			message:  d.Message,
			line:     line,
		}
	}
	return out
}

// lineAt is the line of a byte offset, the last line at the text's end.
func lineAt(snap editor.Snapshot, offset int) int {
	n := snap.Len()
	if n == 0 {
		return 0
	}
	return snap.LineOf(min(max(offset, 0), n-1))
}

// problems lists what the server says of the project's files, sorted by
// file, then by place.
func (j *javaService) problems() []javaProblem {
	var out []javaProblem
	for _, f := range j.diagnostics {
		out = append(out, f.problems...)
	}
	slices.SortStableFunc(out, func(x, y javaProblem) int {
		if x.rel != y.rel {
			return strings.Compare(x.rel, y.rel)
		}
		return x.start - y.start
	})
	return out
}

// problemsLabel is the Problems tab's label: it counts them when there
// are.
func (a *App) problemsLabel() string {
	if n := len(a.java.problems()); n > 0 {
		return fmt.Sprintf("Problems (%d)", n)
	}
	return "Problems"
}

// problemsPage lists every problem the server published: its severity,
// message and place, which a click opens.
func (a *App) problemsPage(c *ui.Context, k *colors) {
	ps := a.java.problems()
	if len(ps) == 0 {
		placeholder(c, k, "No problems")
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Padding(6).Gap(1).Children(func() {
			for i, p := range ps {
				c.Key(fmt.Sprintf("%s:%d:%d", p.rel, p.start, i))
				loc := fmt.Sprintf("%s:%d", p.rel, p.line+1)
				row := ui.Row(c).Height(24).Gap(8).Padding(0, 8).AlignItems(ui.Center).Radius(6).
					Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(loc)
				if row.Hovered() {
					row.Background(k.hover)
				}
				row.Children(func() {
					ui.Box(c).Size(7, 7).Radius(3.5).Shrink(0).Background(severityColor(c, p.severity))
					ui.Text(c, p.message).FontSize(12.5).TextColor(k.text).SingleLine().Ellipsis("…")
					ui.Spacer(c)
					ui.Text(c, loc).FontSize(11.5).TextColor(k.textFaint).SingleLine()
				})
				row.OnClick(func() { a.openAt(p.rel, p.line) })
			}
		})
	})
}

// severityColor colors a problem's dot, as the editor colors its text.
func severityColor(c *ui.Context, sev editor.Severity) ui.Color {
	th := c.Theme()
	switch sev {
	case editor.SeverityError:
		return th.Danger
	case editor.SeverityWarning:
		return th.Warning
	case editor.SeverityInfo:
		return th.Accent
	}
	return th.TextMuted
}

// javaStatus shows how far the server is: downloading what it needs,
// importing, ready, or failed with its error.
func (a *App) javaStatus(c *ui.Context, k *colors) {
	j := &a.java
	if j.status == javaOff {
		return
	}
	th := c.Theme()
	loud := func(iconName string, col ui.Color, text string) ui.Element {
		var e ui.Element
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon(iconName)).Size(12, 12).TextColor(col)
			e = ui.Text(c, text).FontSize(11.5).FontWeight(600).TextColor(k.text).SingleLine()
		})
		return e
	}
	switch j.status {
	case javaStarting:
		if !j.downloading {
			loud("refresh-cw", th.Accent, "jdtls: starting")
			return
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon("download")).Size(12, 12).TextColor(th.Accent)
			ui.Text(c, downloadText(j.progress)).FontSize(11.5).FontWeight(600).TextColor(k.text).SingleLine()
			if j.progress.Total > 0 {
				progressBar(c, k, th.Accent, float32(j.progress.Done)/float32(j.progress.Total))
			}
		})
	case javaImporting:
		text := j.message
		if text == "" {
			text = "jdtls: importing"
		}
		loud("refresh-cw", th.Accent, text)
	case javaReady:
		loud("circle-check", th.Success, "jdtls: ready")
	case javaFailed:
		e := loud("circle-alert", th.Danger, "jdtls: failed")
		if j.err != nil {
			e.Tooltip(j.err.Error())
		}
	}
}

// downloadText says how far a download is, as the status bar shows it.
func downloadText(p setup.Progress) string {
	switch {
	case p.Name == "":
		return "Preparing jdtls…"
	case p.Total > 0:
		return fmt.Sprintf("Downloading %s %d%%", p.Name, percent(p))
	default:
		return fmt.Sprintf("Downloading %s %d MB", p.Name, percent(p))
	}
}

// syncStatus offers to sync the build when one of its files changed,
// which jdtls imports only when told.
func (a *App) syncStatus(c *ui.Context, k *colors) {
	if !a.java.buildChanged {
		return
	}
	th := c.Theme()
	e := ui.Text(c, "Build files changed — Sync").FontSize(11.5).FontWeight(600).TextColor(th.Accent).
		Underline().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Build files changed — Sync")
	e.OnClick(a.syncBuild)
}

// syncBuild tells the language server the project's build files changed,
// which imports it again, and takes the offer away.
func (a *App) syncBuild() {
	j := &a.java
	root := a.project
	if root == "" {
		return
	}
	paths := buildPaths(root)
	if len(paths) == 0 {
		j.buildChanged = false
		return
	}
	srv := j.srv
	if srv == nil {
		j.buildChanged = false
		return
	}
	a.host.Run(func() func() {
		err := srv.BuildFilesChanged(paths...)
		return func() {
			if a.project != root {
				return
			}
			if err != nil {
				// The status bar says jdtls failed, whose tooltip is why.
				j.err, j.status = err, javaFailed
				return
			}
			j.buildChanged = false
			j.buildSigs = buildSignatures(root)
		}
	})
}

// fileSig is a file's size and time, which an edit of it changes.
type fileSig struct {
	size int64
	mod  int64
}

// buildPaths are the project's build files, at its top, which jdtls
// imports from and which a sync names.
func buildPaths(root string) []string {
	var out []string
	for _, name := range []string{"pom.xml", "build.gradle", "build.gradle.kts", ".classpath", ".project"} {
		if p := filepath.Join(root, name); isFile(p) {
			out = append(out, p)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(root, "settings.gradle*")); len(m) > 0 {
		for _, p := range m {
			if isFile(p) {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// buildSignatures says how the build files are now.
func buildSignatures(root string) map[string]fileSig {
	sigs := map[string]fileSig{}
	for _, p := range buildPaths(root) {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		sigs[p] = fileSig{size: fi.Size(), mod: fi.ModTime().UnixNano()}
	}
	return sigs
}

// noteBuildChanges marks the build as changed when a hint says a build
// file, or the folder holding them, changed on disk.
func (a *App) noteBuildChanges(dirs []string) {
	j := &a.java
	if j.project == "" || j.buildSigs == nil {
		return
	}
	// The build files are the project's own: only a hint at its root, or
	// at no folder at all, can be one of theirs.
	if !slices.ContainsFunc(dirs, func(d string) bool { return d == "." || d == "" }) {
		return
	}
	sigs := buildSignatures(j.project)
	if len(sigs) != len(j.buildSigs) {
		j.buildChanged = true
		return
	}
	for p, was := range sigs {
		if j.buildSigs[p] != was {
			j.buildChanged = true
			return
		}
	}
}

// setInlays shows each problem's first line at the end of its line, when
// the settings ask for it. The diagnostics are the editor's own, which
// SetDiagnostics moved through the edits since: they need no rebuild as
// the text is typed, only as problems arrive or the setting changes.
func (a *App) setInlays(t *tab) {
	if t == nil || t.ed == nil {
		return
	}
	j := &a.java
	if !isJavaPath(t.path) || !prefs.InlineErrors {
		delete(j.inlays, t.path)
		a.applyInlays(t)
		return
	}
	snap := t.ed.Buffer.Snapshot()
	ds := t.ed.Diagnostics()
	inlays := make([]editor.Inlay, 0, len(ds))
	for _, d := range ds {
		line := lineAt(snap, d.Start)
		inlays = append(inlays, editor.Inlay{Offset: snap.LineEnd(line), Text: " " + clip(firstLine(d.Message), 80), Trailing: true})
	}
	j.inlays[t.path] = inlays
	a.applyInlays(t)
}

// applyInlineSettings shows or clears the problems in every Java tab, as
// the setting changed.
func (a *App) applyInlineSettings() {
	for _, t := range a.tabs {
		if t.ed != nil && isJavaPath(t.path) {
			a.setInlays(t)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// clip keeps s within n characters, its end an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n-1)]) + "…"
}

// openLine is a file to open and the line to put the caret on once its
// tab shows, as a problem's or a definition's.
type openLine struct {
	path string
	line int
	// col and n select n bytes from byte col of the line, as a search's
	// match; n 0 puts the caret at col, as on a definition's name.
	col, n int
}

// openAt opens a project file and puts the caret on a line: the tree's
// node when it shows the file, the disk's text otherwise. The caret waits
// for the tab's editor, which is where it is scrolled.
func (a *App) openAt(rel string, line int) { a.openSpan(openLine{path: rel, line: line}) }

func (a *App) openSpan(at openLine) {
	a.pendingLine = &at
	a.openRel(at.path)
}

// javaComplete asks the server what completes the text at the caret, and
// shows it when the tab and its text are the ones asked about still.
func (a *App) javaComplete(t *tab, r editor.CompletionRequest) {
	j := &a.java
	srv := j.srv
	if srv == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	file := a.abs(t.path)
	a.host.Run(func() func() {
		start, items, err := srv.Complete(file, r.Offset)
		if err != nil {
			return func() {}
		}
		cs := make([]editor.Completion, len(items))
		for i, it := range items {
			cs[i] = javaCompletion(it)
		}
		return func() {
			if j.srv != srv || !a.tabActive(t) || t.ed.Buffer.Version() != r.Version {
				return
			}
			t.ed.ShowCompletions(start, cs)
		}
	})
}

// javaCompletion maps a server's completion to the editor's.
func javaCompletion(it javalang.Item) editor.Completion {
	c := editor.Completion{
		Label:         it.Label,
		Detail:        it.Detail,
		Kind:          javaCompletionKind(it.Kind),
		Insert:        it.Insert,
		Documentation: it.Documentation,
		FilterText:    it.FilterText,
	}
	if it.Snippet {
		if snip, err := editor.ParseSnippet(it.Insert); err == nil {
			c.Snippet = snip
		}
	}
	return c
}

// javaCompletionKind maps LSP's completion kinds to the editor's.
func javaCompletionKind(kind int) editor.CompletionKind {
	switch kind {
	case 2, 3, 4: // method, function, constructor
		return editor.CompletionMethod
	case 5:
		return editor.CompletionField
	case 6, 12: // variable, value
		return editor.CompletionVariable
	case 7, 8, 13, 22, 25: // class, interface, enum, struct, type parameter
		return editor.CompletionType
	case 9:
		return editor.CompletionModule
	case 14:
		return editor.CompletionKeyword
	case 15:
		return editor.CompletionSnippet
	case 21:
		return editor.CompletionConstant
	}
	return editor.CompletionText
}

// javaHover asks the server what the text at the caret is, and shows it.
func (a *App) javaHover(t *tab, r editor.HoverRequest) {
	j := &a.java
	srv := j.srv
	if srv == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	file := a.abs(t.path)
	a.host.Run(func() func() {
		start, end, text, err := srv.Hover(file, r.Offset)
		if err != nil || text == "" {
			return func() {}
		}
		return func() {
			if j.srv != srv || !a.tabActive(t) || t.ed.Buffer.Version() != r.Version {
				return
			}
			t.ed.ShowHover(start, end, "", hoverBuild(text))
		}
	})
}

// javaDefinition asks the server where the thing at the caret is defined,
// and opens the first of its files, in the project. A method with no body,
// an interface's or an abstract one's, is not where its code is: its
// implementations in the project are, as IntelliJ goes, to the one or to a
// list of them. On the declaration itself, where a go to would stay, it
// lists the usages instead.
func (a *App) javaDefinition(t *tab, offset int) { a.javaGoTo(t, offset, false) }

// GoToImplementation goes to the implementations of what the caret is on,
// whatever kind of method it is, as javaDefinition does of a method with
// no body.
func (a *App) GoToImplementation() {
	if len(a.tabs) == 0 {
		return
	}
	if t := a.tabs[a.active]; t.ed != nil {
		a.javaGoTo(t, t.ed.Cursor(), true)
	}
}

// implementationWait is how long a go to waits for the implementation
// before it takes the definition.
var implementationWait = 1500 * time.Millisecond

// javaGoTo is javaDefinition, which looks for the implementation only of
// a method with no body, unless it is told to for any.
func (a *App) javaGoTo(t *tab, offset int, anyImpl bool) {
	j := &a.java
	srv := j.srv
	if srv == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	file := a.abs(t.path)
	version := t.ed.Buffer.Version()
	current := func() bool { return j.srv == srv && a.tabActive(t) && t.ed.Buffer.Version() == version }
	// here is whether l is where the go to was asked from: the
	// declaration's name, which the definition of a declaration is.
	here := func(l javalang.Location) bool {
		return filepath.Clean(l.Path) == filepath.Clean(file) && l.Start <= offset && offset <= l.End
	}
	a.host.Run(func() func() {
		// Asked together: what the definition says decides whether the
		// implementation counts, but not whether it is asked, and the
		// server takes a request as long as the other's.
		implsc := make(chan []javalang.Location, 1)
		go func() {
			impls, err := srv.Implementation(file, offset)
			if err != nil {
				impls = nil
			}
			implsc <- impls
		}()
		locs, err := srv.Definition(file, offset)
		if err != nil || len(locs) == 0 {
			return func() {}
		}
		loc := locs[0]
		data, _ := os.ReadFile(loc.Path)
		if anyImpl || bodyless(data, loc.Start) {
			// jdtls takes about a minute for its first answer: the
			// definition is what it goes to meanwhile, and the answer, kept,
			// serves the next time.
			var impls []javalang.Location
			select {
			case impls = <-implsc:
			case <-time.After(implementationWait):
			}
			// A class's own method is among its implementations.
			impls = slices.DeleteFunc(impls, here)
			if items := a.goPlaces(impls); len(items) > 0 {
				return func() {
					if current() {
						a.showPicker(t, offset, &picker.List{Title: "Implementations", Items: items})
					}
				}
			}
		}
		if !here(loc) {
			items := a.goPlaces([]javalang.Location{loc})
			return func() {
				if current() && len(items) == 1 {
					a.goToItem(items[0])
				}
			}
		}
		// On the declaration: the usages, which may take a while on a
		// large project, so the list says it is looking meanwhile.
		loading := false
		a.host.Update(func() {
			if current() {
				loading = true
				a.showPicker(t, offset, &picker.List{Title: "Usages", Loading: true})
			}
		})
		refs, err := srv.References(file, offset)
		items := a.goPlaces(refs)
		return func() {
			// Escape on the list while it looked is a change of mind.
			if !current() || loading && !t.ed.PopupShown() {
				return
			}
			a.showPicker(t, offset, &picker.List{Title: "Usages", Items: items})
		}
	})
}

// bodyless reports whether the method declared at offset, the start of its
// name, ends in ';' rather than a body: it is an interface's or an abstract
// one. Anything else it finds, a field or a type, is not.
func bodyless(src []byte, offset int) bool {
	if offset < 0 || offset >= len(src) {
		return false
	}
	depth := 0
	seenParen := false
	for _, c := range src[offset:] {
		switch c {
		case '(':
			depth++
			seenParen = true
		case ')':
			depth--
		case '{', '=':
			if depth == 0 {
				return false
			}
		case ';':
			if depth == 0 {
				return seenParen
			}
		}
	}
	return false
}

// lineOfBytes is the line of a byte offset in data, counted in its
// newlines.
func lineOfBytes(data []byte, offset int) int {
	return strings.Count(string(data[:min(max(offset, 0), len(data))]), "\n")
}

// tabActive reports whether a tab is the one shown.
func (a *App) tabActive(t *tab) bool {
	return len(a.tabs) > 0 && a.tabs[a.active] == t
}

// startLanguageServer is main's starter: it installs the servers and a
// JDK 25 when the machine has neither, starts jdtls on a session that
// takes its diagnostics and status, and returns the session.
func startLanguageServer(a *App) javaStarter {
	return func(project string, diag func(string, uint64, []javalang.Diagnostic), status func(javalang.Status, string)) (langServer, func(), error) {
		ctx := context.Background()
		j := &a.java
		sess := javalang.New()
		sess.SetHandlers(diag, status)
		progress := func(p setup.Progress) {
			a.host.Update(func() { j.progress, j.downloading = p, true })
		}
		if err := a.installServers(ctx, progress); err != nil {
			a.host.Update(func() {
				j.progress, j.downloading = setup.Progress{}, false
				a.serversDone(err)
			})
			return nil, nil, err
		}
		a.host.Update(func() { a.serversDone(nil) })
		home, runtimes, err := a.javaJDK(ctx, progress)
		a.host.Update(func() { j.progress, j.downloading = setup.Progress{}, false })
		if err != nil {
			return nil, nil, err
		}
		// A server that never finishes its handshake must not leave the
		// project stuck starting forever.
		startCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		var lombok string
		if jdtls.UsesLombok(project) {
			lombok = jdtls.LombokJar(serversDir())
		}
		srv, err := jdtls.Start(startCtx, jdtls.Config{
			JDK:        home,
			Lombok:     lombok,
			Runtimes:   runtimes,
			Servers:    serversDir(),
			Project:    project,
			Workspaces: filepath.Join(dataDir, "workspaces"),
			Handler:    sess,
		})
		if err != nil {
			return nil, nil, err
		}
		sess.Attach(srv.Conn, srv.Capabilities)
		return sess, func() { srv.Close() }, nil
	}
}

// javaJDK finds a JDK 25 or newer to run jdtls, installing the app's own
// Temurin 25 under servers when the machine has none, and returns it with
// the JDKs a project may target.
func (a *App) javaJDK(ctx context.Context, progress func(setup.Progress)) (jdk.JDK, []jdk.JDK, error) {
	f := jdk.System
	f.Roots = append(slices.Clone(f.Roots), serversDir())
	home, err := f.Find(ctx)
	if err == nil {
		return home, f.Installed(), nil
	}
	art, ok := setup.JDK(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return jdk.JDK{}, nil, err
	}
	shown := int64(-1)
	_, ierr := setup.Ensure(ctx, a.servers.client, serversDir(), art, func(p setup.Progress) {
		if pct := percent(p); pct != shown {
			shown = pct
			progress(p)
		}
	})
	if ierr != nil {
		return jdk.JDK{}, nil, ierr
	}
	home, err = f.Find(ctx)
	if err != nil {
		return jdk.JDK{}, nil, err
	}
	return home, f.Installed(), nil
}
