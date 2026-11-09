// Package setup downloads the servers the IDE runs, jdtls and java-debug,
// into the app's data folder, once, checking them against the checksums
// their publishers give.
package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Artifact is a server at a version, pinned with its download's sha256:
// a new version is a change of the code, not of what a URL serves.
type Artifact struct {
	Name, Version string
	URL           string
	SHA256        string
	// unpack installs the checked download, archive, into the folder dir.
	unpack func(archive, dir string) error
}

// From is a downloaded from another place, as a mirror, which serves a
// download of that checksum.
func (a Artifact) From(url, sha256 string) Artifact {
	a.URL, a.SHA256 = url, sha256
	return a
}

// Dir is where a is installed under root.
func (a Artifact) Dir(root string) string {
	return filepath.Join(root, a.Name+"-"+a.Version)
}

// JDTLS is Eclipse's milestone build of the Java language server.
var JDTLS = Artifact{
	Name:    "jdtls",
	Version: "1.63.0",
	URL:     "https://download.eclipse.org/jdtls/milestones/1.63.0/jdt-language-server-1.63.0-202610191322.tar.gz",
	SHA256:  "b1f2abe0b3cccb462cf64fb4a5587c7d09df60cf8be7748b152cbd89d018bdc4",
	unpack:  untar,
}

// JavaDebug is the java-debug plugin of jdtls, as VS Code's Java debugger
// ships it: Maven Central's stopped at 0.53.1 in 2024.
var JavaDebug = Artifact{
	Name:    "java-debug",
	Version: "0.53.2",
	URL:     "https://open-vsx.org/api/vscjava/vscode-java-debug/0.60.0/file/vscjava.vscode-java-debug-0.60.0.vsix",
	SHA256:  "f09ebe358a7b08b3c17b21b8d8207b0649acad120eb6f1604dc4f15379f59d66",
	unpack:  zipEntry("extension/server/" + JavaDebugJar),
}

// JavaDebugJar is the plugin's file in JavaDebug's folder.
const JavaDebugJar = "com.microsoft.java.debug.plugin-0.53.2.jar"

// JDK is the Temurin 25 build jdtls runs on, for a system, or false where
// none is pinned. It is installed only where no JDK 25 is: its tarball's
// one top folder is dropped, so that Dir holds Contents/Home on macOS and
// bin/ on Linux.
func JDK(goos, goarch string) (Artifact, bool) {
	const base = "https://github.com/adoptium/temurin25-binaries/releases/download/jdk-25.0.5%2B8/OpenJDK25U-jdk_"
	sums := map[string]string{
		"darwin/arm64": "36dc14d797aaff6d89aab302cd053fdba6207dfdcf772052fa7b023a94b0e857",
		"darwin/amd64": "32cd4d2151c91aa00d0fc911edfd05245ed40fa6b1aa894d5f35c928cd67a59d",
		"linux/arm64":  "bb5b2ded252f21c6090cf780b0a408ad0d83bbb557e702955643557f13231880",
		"linux/amd64":  "68982529869257d0ee6b824ec5aa270ff8b118bb438670da0b97ebdab178d7fb",
	}
	sum, ok := sums[goos+"/"+goarch]
	if !ok {
		return Artifact{}, false
	}
	arch := map[string]string{"arm64": "aarch64", "amd64": "x64"}[goarch]
	osName := map[string]string{"darwin": "mac", "linux": "linux"}[goos]
	return Artifact{
		Name:    "jdk",
		Version: "25.0.5",
		URL:     base + arch + "_" + osName + "_hotspot_25.0.5_8.tar.gz",
		SHA256:  sum,
		unpack:  untarStrip(1),
	}, true
}

// Artifacts are what the IDE needs, in the order they download.
var Artifacts = []Artifact{JDTLS, JavaDebug}

// Progress is how far a download is: Total is -1 while unknown.
type Progress struct {
	Name        string
	Done, Total int64
}

// Ensure installs a under root unless it is there, and returns its folder.
// It downloads to a temporary file, checks it, and unpacks it beside the
// folder before renaming it there: a folder there is a whole install, and
// what an interrupted one leaves is redone.
func Ensure(ctx context.Context, client *http.Client, root string, a Artifact, progress func(Progress)) (string, error) {
	dir := a.Dir(root)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	// What a run that quit midway left.
	stale, _ := filepath.Glob(filepath.Join(root, "."+a.Name+"-*"))
	for _, p := range stale {
		os.RemoveAll(p)
	}
	archive, err := download(ctx, client, root, a, progress)
	if err != nil {
		return "", fmt.Errorf("download %s %s: %w", a.Name, a.Version, err)
	}
	defer os.Remove(archive)
	tmp, err := os.MkdirTemp(root, "."+a.Name+"-")
	if err != nil {
		return "", err
	}
	if err := a.unpack(archive, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("unpack %s %s: %w", a.Name, a.Version, err)
	}
	// MkdirTemp's folder is the user's alone; an install is as others.
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return dir, nil
}

// ErrChecksum is a download whose sha256 isn't the one pinned.
var ErrChecksum = errors.New("checksum mismatch")

func download(ctx context.Context, client *http.Client, root string, a Artifact, progress func(Progress)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", a.URL, resp.Status)
	}
	f, err := os.CreateTemp(root, "."+a.Name+"-*.download")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	p := Progress{Name: a.Name, Total: resp.ContentLength}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err = f.Write(buf[:n]); err != nil {
				break
			}
			h.Write(buf[:n])
			p.Done += int64(n)
			progress(p)
		}
		if rerr == io.EOF {
			break
		}
		if err = rerr; err != nil {
			break
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		err = ErrChecksum
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// inside joins an archive's name to dir, refusing one that leaves it, as
// ../x or /x, which an archive must not write.
func inside(dir, name string) (string, error) {
	clean := path.Clean(filepath.ToSlash(name))
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("bad path %q in archive", name)
	}
	return filepath.Join(dir, filepath.FromSlash(clean)), nil
}

// untar unpacks a .tar.gz, with its files' permissions: jdtls's launcher
// is executable.
func untar(archive, dir string) error { return untarStrip(0)(archive, dir) }

// untarStrip is untar without the first n folders of every name, as tar's
// --strip-components: an entry left with no name, the folder itself, is
// skipped.
func untarStrip(n int) func(archive, dir string) error {
	return func(archive, dir string) error { return untarInto(archive, dir, n) }
}

func untarInto(archive, dir string, strip int) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := hdr.Name
		if strip > 0 {
			parts := strings.SplitN(path.Clean(filepath.ToSlash(name)), "/", strip+1)
			if len(parts) <= strip {
				continue
			}
			name = parts[strip]
		}
		p, err := inside(dir, name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(p, 0o755)
		case tar.TypeReg:
			err = writeFile(p, tr, hdr.FileInfo().Mode().Perm())
		default:
			// Links and devices: jdtls and the JDK's tarballs ship none.
		}
		if err != nil {
			return err
		}
	}
}

// zipEntry unpacks one file of a zip, as a jar of a VS Code extension.
func zipEntry(name string) func(archive, dir string) error {
	return func(archive, dir string) error {
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name != name {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			defer r.Close()
			return writeFile(filepath.Join(dir, path.Base(name)), r, 0o644)
		}
		return fmt.Errorf("%s not in the archive", name)
	}
}

func writeFile(p string, r io.Reader, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
