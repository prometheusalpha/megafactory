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
	a, b := fakeJDK(t, "21.0.1"), fakeJDK(t, "23")
	j, err := finder(map[string]string{"JAVA_HOME": a}, b, "").Find(context.Background())
	if err != nil || j.Home != a || j.Major != 21 || j.Java() != filepath.Join(a, "bin", "java") {
		t.Errorf("%+v, %v", j, err)
	}
}

func TestFindSkipsOldJavaHome(t *testing.T) {
	old, ok := fakeJDK(t, "17.0.9"), fakeJDK(t, "21.0.2")
	j, err := finder(map[string]string{"JAVA_HOME": old}, ok, "").Find(context.Background())
	if err != nil || j.Home != ok {
		t.Errorf("%+v, %v", j, err)
	}
}

func TestFindFollowsPathLink(t *testing.T) {
	home := fakeJDK(t, "21.0.2")
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
