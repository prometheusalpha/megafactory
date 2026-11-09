package jdk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeJDK(t *testing.T, version string) string {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "bin"), 0o755)
	os.WriteFile(filepath.Join(home, "release"), []byte("IMPLEMENTOR=\"X\"\nJAVA_VERSION=\""+version+"\"\n"), 0o644)
	os.WriteFile(filepath.Join(home, "bin", "java"), nil, 0o755)
	return home
}

func finder(env map[string]string, javaHome string, onPath string) Finder {
	return Finder{
		Getenv: func(k string) string { return env[k] },
		JavaHome: func(context.Context, ...string) (string, error) {
			if javaHome == "" {
				return "", errors.New("no JVM")
			}
			return javaHome, nil
		},
		LookPath: func(string) (string, error) {
			if onPath == "" {
				return "", errors.New("not found")
			}
			return onPath, nil
		},
	}
}

func TestMajor(t *testing.T) {
	for v, want := range map[string]int{"21.0.13.0.2": 21, "17": 17, "1.8.0_402": 8, "25-ea": 25} {
		if got, err := Major(v); err != nil || got != want {
			t.Errorf("Major(%q) = %d, %v; want %d", v, got, err, want)
		}
	}
	if _, err := Major("abc"); err == nil {
		t.Error("Major(abc) passed")
	}
}

func TestFindPrefersJavaHome(t *testing.T) {
	a, b := fakeJDK(t, "25.0.1"), fakeJDK(t, "27")
	j, err := finder(map[string]string{"JAVA_HOME": a}, b, "").Find(context.Background())
	if err != nil || j.Home != a || j.Major != 25 || j.Java() != filepath.Join(a, "bin", "java") {
		t.Errorf("%+v, %v", j, err)
	}
}

func TestFindSkipsOldJavaHome(t *testing.T) {
	old, ok := fakeJDK(t, "17.0.9"), fakeJDK(t, "25.0.2")
	j, err := finder(map[string]string{"JAVA_HOME": old}, ok, "").Find(context.Background())
	if err != nil || j.Home != ok {
		t.Errorf("%+v, %v", j, err)
	}
}

func TestFindFollowsPathLink(t *testing.T) {
	home := fakeJDK(t, "25.0.2")
	link := filepath.Join(t.TempDir(), "java")
	os.Symlink(filepath.Join(home, "bin", "java"), link)
	j, err := finder(nil, "", link).Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// EvalSymlinks may spell the temp folder differently.
	if got, _ := filepath.EvalSymlinks(home); j.Home != got {
		t.Errorf("home %s, want %s", j.Home, got)
	}
}

func TestFindErrors(t *testing.T) {
	_, err := finder(nil, "", "").Find(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no JDK found") || !strings.Contains(err.Error(), "JAVA_HOME") {
		t.Errorf("none: %v", err)
	}
	old := fakeJDK(t, "17.0.9")
	_, err = finder(map[string]string{"JAVA_HOME": old}, "", "").Find(context.Background())
	if err == nil || !strings.Contains(err.Error(), "17.0.9") || !strings.Contains(err.Error(), old) {
		t.Errorf("too old: %v", err)
	}
	// A home without a release file is not a JDK.
	_, err = finder(map[string]string{"JAVA_HOME": t.TempDir()}, "", "").Find(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no JDK found") {
		t.Errorf("not a JDK: %v", err)
	}
}

func TestSystemFindsThisMachinesJDK(t *testing.T) {
	if _, err := os.Stat("/usr/libexec/java_home"); err != nil {
		t.Skip("not macOS")
	}
	if j, err := System.Find(context.Background()); err != nil {
		t.Skip(err)
	} else if j.Major < MinMajor {
		t.Errorf("%+v", j)
	}
}

func TestInstalledKeepsNewestPerVersion(t *testing.T) {
	root := t.TempDir()
	// macOS lays a JDK out under Contents/Home; others as the home itself.
	mac := func(name, version string) {
		h := filepath.Join(root, name, "Contents", "Home")
		os.MkdirAll(h, 0o755)
		os.WriteFile(filepath.Join(h, "release"), []byte("JAVA_VERSION=\""+version+"\"\n"), 0o644)
	}
	mac("a", "21.0.9")
	mac("b", "21.0.13")
	mac("c", "17.0.1")
	flat := filepath.Join(root, "d")
	os.MkdirAll(flat, 0o755)
	os.WriteFile(filepath.Join(flat, "release"), []byte("JAVA_VERSION=\"25.0.5\"\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "not-a-jdk"), 0o755)

	f := finder(nil, "", "")
	f.Roots = []string{root, filepath.Join(root, "missing")}
	var got []string
	for _, j := range f.Installed() {
		got = append(got, j.Version)
	}
	if want := "17.0.1 21.0.13 25.0.5"; strings.Join(got, " ") != want {
		t.Errorf("installed %v, want %s", got, want)
	}
}

func TestRuntimeName(t *testing.T) {
	for n, want := range map[int]string{8: "JavaSE-1.8", 11: "JavaSE-11", 21: "JavaSE-21"} {
		if got := RuntimeName(n); got != want {
			t.Errorf("RuntimeName(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestFindUsesRoots(t *testing.T) {
	root := t.TempDir()
	// What the app installs: the JDK's home, in a folder of its own.
	h := filepath.Join(root, "jdk-25.0.5", "Contents", "Home")
	os.MkdirAll(h, 0o755)
	os.WriteFile(filepath.Join(h, "release"), []byte("JAVA_VERSION=\"25.0.5\"\n"), 0o644)
	f := finder(nil, "", "")
	f.Roots = []string{root}
	j, err := f.Find(context.Background())
	if err != nil || j.Home != h {
		t.Errorf("%+v, %v", j, err)
	}
}
