// Package build finds the build system of a project and the tasks it
// offers: Maven's lifecycle phases and Gradle's tasks as its wrapper
// lists them.
package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Kind is the build system a project uses.
type Kind int

const (
	KindNone Kind = iota
	KindMaven
	KindGradle
)

// Project is a folder and the build system it holds.
type Project struct {
	Root string
	Kind Kind
}

// Detect returns the build of the project at root: Maven when pom.xml is
// there, else Gradle when build.gradle or build.gradle.kts is there.
func Detect(root string) (Project, error) {
	p := Project{Root: root}
	for _, marker := range []struct {
		name string
		kind Kind
	}{
		{"pom.xml", KindMaven},
		{"build.gradle", KindGradle},
		{"build.gradle.kts", KindGradle},
	} {
		_, err := os.Stat(filepath.Join(root, marker.name))
		switch {
		case err == nil:
			p.Kind = marker.kind
			return p, nil
		case !errors.Is(err, os.ErrNotExist):
			return p, err
		}
	}
	return p, nil
}

// Command is the launcher and args for a task, e.g. {"./mvnw", "test"}:
// the wrapper when present and executable, else mvn/gradle from PATH.
func (p Project) Command(args ...string) ([]string, error) {
	var wrapper, name string
	switch p.Kind {
	case KindMaven:
		wrapper, name = "mvnw", "mvn"
	case KindGradle:
		wrapper, name = "gradlew", "gradle"
	default:
		return nil, fmt.Errorf("no build in %s", p.Root)
	}
	// A wrapper need only be executable for this user: a checkout may
	// have lost the other bits.
	w := filepath.Join(p.Root, wrapper)
	if fi, err := os.Stat(w); err == nil && fi.Mode().Perm()&0o111 != 0 {
		abs, err := filepath.Abs(w)
		if err != nil {
			return nil, err
		}
		return append([]string{abs}, args...), nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	return append([]string{path}, args...), nil
}

// Task is one task a build offers.
type Task struct {
	Name        string
	Description string
}

// Tasks lists the build's tasks. Maven's are its lifecycle phases, plus
// spring-boot:run when the pom declares spring-boot-maven-plugin;
// Gradle's are read from the wrapper's `tasks --all` output (honouring
// ctx).
func (p Project) Tasks(ctx context.Context) ([]Task, error) {
	switch p.Kind {
	case KindMaven:
		return mavenTasks(p.Root)
	case KindGradle:
		return gradleTasks(ctx, p)
	default:
		return nil, fmt.Errorf("no build in %s", p.Root)
	}
}
