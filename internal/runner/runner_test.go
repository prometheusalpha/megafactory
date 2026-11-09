package runner

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestChildProcess is the deterministic program Start runs, as jdtls's
// TestMain does: the test binary re-execs itself with HELPER_CASE set.
func TestChildProcess(t *testing.T) {
	c := os.Getenv("HELPER_CASE")
	if c == "" {
		return
	}
	switch c {
	case "streams":
		fmt.Fprintln(os.Stdout, "out one")
		fmt.Fprintln(os.Stdout, "out two")
		fmt.Fprintln(os.Stderr, "err one")
	case "partial":
		fmt.Fprint(os.Stdout, "no newline")
		fmt.Fprint(os.Stderr, "also no newline")
	case "wait":
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func helperArgv(t *testing.T, env string) Spec {
	t.Helper()
	return Spec{
		Argv: []string{os.Args[0], "-test.run=TestChildProcess"},
		Env:  []string{"HELPER_CASE=" + env},
	}
}

type collected struct {
	mu    sync.Mutex
	lines []Line
	exits int
}

func (c *collected) line(l Line) {
	c.mu.Lock()
	c.lines = append(c.lines, l)
	c.mu.Unlock()
}

func (c *collected) exit(error) {
	c.mu.Lock()
	c.exits++
	c.mu.Unlock()
}

func (c *collected) snapshot() ([]Line, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Line(nil), c.lines...), c.exits
}

func waitExit(t *testing.T, c *collected) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, n := c.snapshot()
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("onExit never ran")
}

func TestStartKeepsStreamsSeparate(t *testing.T) {
	c := &collected{}
	p, err := Start(helperArgv(t, "streams"), c.line, c.exit)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	waitExit(t, c)
	lines, exits := c.snapshot()
	if exits != 1 {
		t.Errorf("onExit ran %d times", exits)
	}
	var out, errLines []string
	for _, l := range lines {
		if l.Stream == Stdout {
			out = append(out, l.Text)
		} else {
			errLines = append(errLines, l.Text)
		}
	}
	if len(out) != 2 || out[0] != "out one" || out[1] != "out two" {
		t.Errorf("stdout %v", out)
	}
	if len(errLines) != 1 || errLines[0] != "err one" {
		t.Errorf("stderr %v", errLines)
	}
}

func TestStartFlushesTrailingPartialLine(t *testing.T) {
	c := &collected{}
	p, err := Start(helperArgv(t, "partial"), c.line, c.exit)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	waitExit(t, c)
	lines, _ := c.snapshot()
	if len(lines) != 2 {
		t.Fatalf("lines %v", lines)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		seen[l.Text] = true
	}
	if !seen["no newline"] || !seen["also no newline"] {
		t.Errorf("partial lines lost: %v", lines)
	}
}

func TestStopEndsProcessAndFiresOnExitOnce(t *testing.T) {
	c := &collected{}
	p, err := Start(helperArgv(t, "wait"), c.line, c.exit)
	if err != nil {
		t.Fatal(err)
	}
	p.Stop()
	p.Stop() // idempotent
	waitExit(t, c)
	_, exits := c.snapshot()
	if exits != 1 {
		t.Errorf("onExit ran %d times", exits)
	}
}

func TestStartRejectsBadArgv(t *testing.T) {
	c := &collected{}
	if _, err := Start(Spec{}, c.line, c.exit); err == nil {
		t.Error("empty argv started")
	}
	if _, err := Start(Spec{Argv: []string{"megafactory-no-such-binary"}}, c.line, c.exit); err == nil {
		t.Error("missing binary started")
	}
}
