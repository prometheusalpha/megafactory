package setup

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		mode := int64(0o644)
		if strings.HasPrefix(name, "bin/") {
			mode = 0o755
		}
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// serve serves body at /a, counting the requests.
func serve(t *testing.T, body []byte) (*httptest.Server, *int) {
	n := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, n
}

func TestEnsureInstallsOnce(t *testing.T) {
	body := tarGz(t, map[string]string{"bin/jdtls": "#!/bin/sh\n", "plugins/a.jar": "jar"})
	srv, requests := serve(t, body)
	a := Artifact{Name: "jdtls", Version: "9.9", URL: srv.URL + "/a", SHA256: sum(body), unpack: untar}
	root := t.TempDir()
	// Left by a run that quit midway.
	os.WriteFile(filepath.Join(root, ".jdtls-123.download"), []byte("half"), 0o644)
	var last Progress
	dir, err := Ensure(context.Background(), srv.Client(), root, a, func(p Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "jdtls-9.9") || last.Done != int64(len(body)) || last.Name != "jdtls" {
		t.Errorf("dir %s, progress %+v", dir, last)
	}
	if fi, err := os.Stat(filepath.Join(dir, "bin/jdtls")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("launcher %v, %v", fi, err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Errorf("install folder %v", fi.Mode().Perm())
	}
	if _, err := Ensure(context.Background(), srv.Client(), root, a, func(Progress) {}); err != nil || *requests != 1 {
		t.Errorf("second ensure: %v, %d requests", err, *requests)
	}
	// Nothing is left beside the install.
	if es, _ := os.ReadDir(root); len(es) != 1 {
		t.Errorf("root holds %d entries", len(es))
	}
}

func TestEnsureRefusesWrongChecksum(t *testing.T) {
	body := tarGz(t, map[string]string{"x": "y"})
	srv, _ := serve(t, body)
	a := Artifact{Name: "jdtls", Version: "1", URL: srv.URL + "/a", SHA256: sum([]byte("other")), unpack: untar}
	root := t.TempDir()
	_, err := Ensure(context.Background(), srv.Client(), root, a, func(Progress) {})
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err %v", err)
	}
	if es, _ := os.ReadDir(root); len(es) != 0 {
		t.Errorf("left %d entries", len(es))
	}
}

func TestEnsureFailsCleanly(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	a := Artifact{Name: "jdtls", Version: "1", URL: srv.URL + "/a", SHA256: "x", unpack: untar}
	if _, err := Ensure(context.Background(), srv.Client(), t.TempDir(), a, func(Progress) {}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err %v", err)
	}
	// A bad archive leaves no folder to take for an install.
	body := []byte("not a tarball")
	srv2, _ := serve(t, body)
	root := t.TempDir()
	a = Artifact{Name: "jdtls", Version: "1", URL: srv2.URL + "/a", SHA256: sum(body), unpack: untar}
	if _, err := Ensure(context.Background(), srv2.Client(), root, a, func(Progress) {}); err == nil {
		t.Error("unpacked garbage")
	}
	if es, _ := os.ReadDir(root); len(es) != 0 {
		t.Errorf("left %d entries", len(es))
	}
}

func TestUntarRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../evil", "/etc/evil", "a/../../evil"} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		tw.Write([]byte("x"))
		tw.Close()
		gz.Close()
		f := filepath.Join(t.TempDir(), "a.tar.gz")
		os.WriteFile(f, buf.Bytes(), 0o644)
		dir := t.TempDir()
		if err := untar(f, dir); err == nil {
			t.Errorf("%s unpacked", name)
		}
	}
}

func TestZipEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("extension/server/" + JavaDebugJar)
	w.Write([]byte("jar"))
	zw.Close()
	f := filepath.Join(t.TempDir(), "x.vsix")
	os.WriteFile(f, buf.Bytes(), 0o644)
	dir := t.TempDir()
	if err := JavaDebug.unpack(f, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, JavaDebugJar)); string(b) != "jar" {
		t.Errorf("jar %q", b)
	}
	if err := zipEntry("missing.jar")(f, t.TempDir()); err == nil {
		t.Error("found a missing entry")
	}
}
