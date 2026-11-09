// Package jdk finds the JDK that runs jdtls, which needs Java 21 or newer.
package jdk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// MinMajor is the oldest Java that runs the jdtls we ship.
const MinMajor = 21

// JDK is an installation, with the version its release file names.
type JDK struct {
	Home    string
	Version string
	Major   int
}

// Java is the launcher to run jdtls with.
func (j JDK) Java() string { return filepath.Join(j.Home, "bin", "java") }

// Finder reads the machine; tests swap its fields.
type Finder struct {
	Getenv func(string) string
	// JavaHome runs /usr/libexec/java_home with args, which lists the
	// installed JDKs on macOS beyond the one JAVA_HOME points to.
	JavaHome func(ctx context.Context, args ...string) (string, error)
	// LookPath resolves java on PATH.
	LookPath func(string) (string, error)
}

// System is the Finder of this machine.
var System = Finder{
	Getenv: os.Getenv,
	JavaHome: func(ctx context.Context, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "/usr/libexec/java_home", args...).Output()
		return strings.TrimSpace(string(out)), err
	},
	LookPath: exec.LookPath,
}

// Find returns the first JDK of Java 21+, looking at JAVA_HOME, then at
// the newest one macOS knows, then at java on PATH. The error names what
// was found too old, or says none was found, with what to do.
func (f Finder) Find(ctx context.Context) (JDK, error) {
	var tooOld []JDK
	for _, home := range f.candidates(ctx) {
		j, err := Read(home)
		if err != nil {
			continue
		}
		if j.Major >= MinMajor {
			return j, nil
		}
		tooOld = append(tooOld, j)
	}
	if len(tooOld) > 0 {
		j := tooOld[0]
		return JDK{}, fmt.Errorf("jdtls needs JDK %d or newer, found %s at %s: install a newer JDK or point JAVA_HOME to it", MinMajor, j.Version, j.Home)
	}
	return JDK{}, fmt.Errorf("no JDK found: jdtls needs JDK %d or newer, install one or set JAVA_HOME", MinMajor)
}

func (f Finder) candidates(ctx context.Context) []string {
	var homes []string
	if h := f.Getenv("JAVA_HOME"); h != "" {
		homes = append(homes, h)
	}
	if f.JavaHome != nil {
		if h, err := f.JavaHome(ctx, "-v", strconv.Itoa(MinMajor)+"+"); err == nil && h != "" {
			homes = append(homes, h)
		}
	}
	if p, err := f.LookPath("java"); err == nil {
		// java may be a link into the JDK, as Homebrew's is.
		if real, err := filepath.EvalSymlinks(p); err == nil {
			homes = append(homes, filepath.Dir(filepath.Dir(real)))
		}
	}
	return homes
}

// Read describes the JDK at home from its release file, without running
// java. A home without one is not a JDK.
func Read(home string) (JDK, error) {
	f, err := os.Open(filepath.Join(home, "release"))
	if err != nil {
		return JDK{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		v, ok := strings.CutPrefix(sc.Text(), "JAVA_VERSION=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		major, err := Major(v)
		if err != nil {
			return JDK{}, err
		}
		return JDK{Home: home, Version: v, Major: major}, nil
	}
	return JDK{}, errors.New("no JAVA_VERSION in " + filepath.Join(home, "release"))
}

// Major is the feature release of a JAVA_VERSION: "21.0.13" is 21, and
// "1.8.0_402" is 8, as Java 8 still numbered itself.
func Major(version string) (int, error) {
	parts := strings.FieldsFunc(version, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == '+' })
	if len(parts) > 1 && parts[0] == "1" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return 0, fmt.Errorf("bad Java version %q", version)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("bad Java version %q", version)
	}
	return n, nil
}
