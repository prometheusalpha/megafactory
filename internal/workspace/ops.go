package workspace

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The operations take project paths: relative to root, with slashes.

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// Create makes an empty file, or a folder, at rel, and the folders above
// it that are missing, as "com/example/App.java" asks. It fails when
// something is there already.
func Create(root, rel string, dir bool) error {
	p := abs(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if dir {
		return os.Mkdir(p, 0o755)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Rename moves from to to, which must not exist: os.Rename would replace
// a file there. Only the case of a name changed is the same file on
// macOS's disks, and is renamed.
func Rename(root, from, to string) error {
	src, dst := abs(root, from), abs(root, to)
	if fi, err := os.Lstat(dst); err == nil {
		si, err := os.Lstat(src)
		if err != nil {
			return err
		}
		if !os.SameFile(si, fi) {
			return fmt.Errorf("%s already exists", path.Base(to))
		}
	}
	return os.Rename(src, dst)
}

// Copy copies the file or folder from into the folder dir, under a name
// free there, and returns its path: "App copy.java" where "App.java" is
// taken, as Finder names it.
func Copy(root, from, dir string) (string, error) {
	if err := intoItself(from, dir); err != nil {
		return "", err
	}
	to := freeName(root, dir, path.Base(from))
	return to, copyAll(abs(root, from), abs(root, to))
}

// Move moves the file or folder from into the folder dir, under a name
// free there, and returns its path. Into the folder it is in, it stays.
func Move(root, from, dir string) (string, error) {
	if path.Dir(from) == dir || (dir == "" && !strings.Contains(from, "/")) {
		return from, nil
	}
	if err := intoItself(from, dir); err != nil {
		return "", err
	}
	to := freeName(root, dir, path.Base(from))
	return to, os.Rename(abs(root, from), abs(root, to))
}

func intoItself(from, dir string) error {
	if dir == from || strings.HasPrefix(dir, from+"/") {
		return fmt.Errorf("%s can't go inside itself", path.Base(from))
	}
	return nil
}

func freeName(root, dir, name string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" { // a dotfile, as .gitignore
		stem, ext = name, ""
	}
	for i := 0; ; i++ {
		candidate := name
		switch i {
		case 0:
		case 1:
			candidate = stem + " copy" + ext
		default:
			candidate = fmt.Sprintf("%s copy %d%s", stem, i, ext)
		}
		rel := path.Join(dir, candidate)
		if _, err := os.Lstat(abs(root, rel)); errors.Is(err, fs.ErrNotExist) {
			return rel
		}
	}
}

// copyAll copies a file, a symlink as a symlink, or a folder with all in
// it, keeping their permissions.
func copyAll(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, strings.TrimPrefix(p, src))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(p, target, info.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
