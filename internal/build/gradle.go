package build

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// gradleTimeout bounds a task listing: a cold daemon can take a minute.
const gradleTimeout = 120 * time.Second

// gradleTasks asks the wrapper for every task and reads the answer. The
// daemon writes both its progress and the list on the same streams, so
// both are kept aside for the error and parsed for the tasks.
func gradleTasks(ctx context.Context, p Project) ([]Task, error) {
	ctx, cancel := context.WithTimeout(ctx, gradleTimeout)
	defer cancel()
	argv, err := p.Command("tasks", "--all", "--console=plain")
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", strings.Join(argv, " "), err, tail(string(out), 2048))
	}
	return parseGradleTasks(string(out)), nil
}

// tail is the last n bytes of s, marked as cut when it had to be.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// parseGradleTasks reads the tasks out of `gradle tasks --all`'s output.
// A task is a line of an optionally indented name, " - ", and a
// description, as "app:build - Assembles the project." Group headers,
// the help text and option lines have no such shape and are skipped.
func parseGradleTasks(out string) []Task {
	var tasks []Task
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name, desc, ok := strings.Cut(strings.TrimSpace(line), " - ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, "(") {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		tasks = append(tasks, Task{name, strings.TrimSpace(desc)})
	}
	return tasks
}
