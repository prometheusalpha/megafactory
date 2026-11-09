package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"megafactory/internal/setup"

	"github.com/egoist/mygo/ui"
)

// fakeServers serves a java-debug download, failing while fail is set.
func fakeServers(t *testing.T, fail *atomic.Bool) servers {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("extension/server/" + setup.JavaDebugJar)
	w.Write([]byte("jar"))
	zw.Close()
	body := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	return servers{
		artifacts: []setup.Artifact{setup.JavaDebug.From(srv.URL+"/debug.vsix", hex.EncodeToString(sum[:]))},
		client:    srv.Client(),
	}
}

func TestJavaProjectInstallsServers(t *testing.T) {
	// A test of its own folder: an install is kept for the next.
	defer func(d string) { dataDir = d }(dataDir)
	dataDir = t.TempDir()
	var fail atomic.Bool
	fail.Store(true)
	a := newApp(defaultState)
	a.servers = fakeServers(t, &fail)
	var later func()
	a.host.after = func(_ time.Duration, fn func()) { later = fn }
	a.openProject(sampleProject(t))
	tt := ui.NewTester(a.view, 1280, 800)
	if a.servers.phase != serversFailed || !tt.HasText("jdtls download failed") {
		t.Fatalf("phase %d, err %v", a.servers.phase, a.servers.err)
	}
	fail.Store(false)
	click(t, tt, "Retry jdtls download")
	// The end of a download shows a moment, then quiets.
	if a.servers.phase != serversReady || !tt.HasText("jdtls ready") {
		t.Fatalf("after retry: phase %d, err %v", a.servers.phase, a.servers.err)
	}
	later()
	tt.Frame()
	if !tt.HasText("jdtls: installed") {
		t.Error("ready kept showing")
	}
	// Installed already, the next project says nothing of it.
	a.servers.phase = serversIdle
	later = nil
	a.openProject(sampleProject(t))
	tt.Frame()
	if a.servers.phase != serversReady || later != nil || !tt.HasText("jdtls: installed") {
		t.Errorf("phase %d, flashed %v", a.servers.phase, later != nil)
	}
	jar := filepath.Join(serversDir(), "java-debug-"+setup.JavaDebug.Version, setup.JavaDebugJar)
	if _, err := os.Stat(jar); err != nil {
		t.Error(err)
	}
}

func TestOtherProjectsDownloadNothing(t *testing.T) {
	var fail atomic.Bool
	a := newApp(defaultState)
	a.servers = fakeServers(t, &fail)
	dir := filepath.Join(t.TempDir(), "notes")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.md"), nil, 0o644)
	a.openProject(dir)
	if a.servers.phase != serversIdle {
		t.Errorf("phase %d for a project without Java", a.servers.phase)
	}
}

func TestDownloadProgressShows(t *testing.T) {
	a := newApp(defaultState)
	a.openProject(sampleProject(t)) // the status bar shows jdtls's part with a project
	tt := ui.NewTester(a.view, 1280, 800)
	a.servers.phase = serversDownloading
	for _, c := range []struct {
		p    setup.Progress
		want string
	}{
		{setup.Progress{}, "Preparing jdtls…"},
		{setup.Progress{Name: "jdtls", Done: 25, Total: 100}, "Download progress"},
		{setup.Progress{Name: "jdtls", Done: 25, Total: 100}, "Downloading jdtls 25%"},
		{setup.Progress{Name: "jdtls", Done: 3 << 20, Total: -1}, "Downloading jdtls 3 MB"},
	} {
		a.servers.progress = c.p
		tt.Frame()
		if _, ok := tt.Find(c.want); !ok {
			t.Errorf("no %q", c.want)
		}
	}
}
