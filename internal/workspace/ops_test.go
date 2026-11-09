package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func exists(root, rel string) bool {
	_, err := os.Lstat(abs(root, rel))
	return err == nil
}

func TestCreate(t *testing.T) {
	root := t.TempDir()
	if err := Create(root, "src/com/App.java", false); err != nil {
		t.Fatal(err)
	}
	if err := Create(root, "src/com/App.java", false); err == nil {
		t.Error("created over a file")
	}
	if err := Create(root, "docs", true); err != nil || !exists(root, "docs") {
		t.Errorf("folder: %v", err)
	}
}

func TestRenameKeepsWhatIsThere(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "b.txt")
	if err := Rename(root, "a.txt", "b.txt"); err == nil {
		t.Error("renamed over b.txt")
	}
	if err := Rename(root, "a.txt", "A.txt"); err != nil {
		t.Errorf("case rename: %v", err)
	}
	es, _ := List(root, "", false)
	if es[0].Name != "A.txt" {
		t.Errorf("entries %+v", es)
	}
}

func TestCopyAndMove(t *testing.T) {
	root := t.TempDir()
	write(t, root, "App.java", ".gitignore", "pkg/A.java", "pkg/sub/B.java", "dest/x")
	os.Symlink("A.java", filepath.Join(root, "pkg/link"))

	for _, c := range []struct{ from, dir, want string }{
		{"App.java", "", "App copy.java"},
		{"App.java", "", "App copy 2.java"},
		{".gitignore", "", ".gitignore copy"},
		{"pkg", "dest", "dest/pkg"},
	} {
		got, err := Copy(root, c.from, c.dir)
		if err != nil || got != c.want || !exists(root, got) {
			t.Errorf("copy %s into %q: %q, %v; want %q", c.from, c.dir, got, err, c.want)
		}
	}
	if !exists(root, "dest/pkg/sub/B.java") {
		t.Error("folder copied without its folders")
	}
	if l, _ := os.Readlink(abs(root, "dest/pkg/link")); l != "A.java" {
		t.Errorf("symlink copied as %q", l)
	}
	if _, err := Copy(root, "pkg", "pkg/sub"); err == nil {
		t.Error("copied a folder into itself")
	}

	got, err := Move(root, "pkg", "dest")
	if err != nil || got != "dest/pkg copy" || exists(root, "pkg") {
		t.Errorf("move: %q, %v", got, err)
	}
	if got, err := Move(root, "App.java", ""); err != nil || got != "App.java" {
		t.Errorf("move in place: %q, %v", got, err)
	}
}
