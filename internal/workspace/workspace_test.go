package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	write(t, root, ".gitignore", "pom.xml", "debug.log", "target/classes/A.class", "src/main/App.java", "src/main/gen/B.java", "src/.gitignore")
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("target/\n*.log\n"), 0o644)
	os.WriteFile(filepath.Join(root, "src/.gitignore"), []byte("gen/\n"), 0o644)

	es, err := List(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range es {
		got[e.Name] = e
	}
	if es[0].Name != ".git" || !es[0].Dir || !es[0].Ignored {
		t.Errorf("first %+v, want .git, a folder, ignored", es[0])
	}
	for name, want := range map[string]bool{"target": true, "debug.log": true, "src": false, "pom.xml": false, ".gitignore": false} {
		if got[name].Ignored != want {
			t.Errorf("%s ignored %v, want %v", name, got[name].Ignored, want)
		}
	}
	es, _ = List(root, "src/main", false)
	if len(es) != 2 || es[0].Name != "gen" || !es[0].Ignored || es[1].Ignored {
		t.Errorf("src/main %+v", es)
	}
	// Below an ignored folder, all is ignored.
	es, _ = List(root, "target", true)
	if len(es) != 1 || !es[0].Ignored {
		t.Errorf("target %+v", es)
	}
}

func TestListOutsideRepository(t *testing.T) {
	root := t.TempDir()
	write(t, root, "b.txt", "a/x.txt", ".hidden")
	es, err := List(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 || es[0].Name != "a" || es[1].Name != ".hidden" {
		t.Fatalf("%+v", es)
	}
	for _, e := range es {
		if e.Ignored {
			t.Errorf("%s ignored outside a repository", e.Name)
		}
	}
}
