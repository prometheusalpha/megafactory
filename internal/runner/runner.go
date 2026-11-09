// Package runner owns the process a run configuration starts. It keeps
// stdout and stderr separate, so the console can color them differently.
package runner

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Spec is the process to run. Argv[0] is the executable. Env entries are
// "KEY=value" additions over the process environment. Dir is the cwd.
type Spec struct {
	Argv []string
	Dir  string
	Env  []string
}

// Stream names which pipe a Line came from.
type Stream uint8

const (
	Stdout Stream = iota
	Stderr
)

// Line is one line of process output, without its newline.
type Line struct {
	Text   string
	Stream Stream
}

// Process is a running program. Its methods are safe from any goroutine.
type Process struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	stopped bool
	waited  bool
	once    sync.Once
}

// Start launches spec. onLine runs for each output line and onExit exactly
// once when the process ends; both may run on goroutines owned by Process.
func Start(spec Spec, onLine func(Line), onExit func(error)) (*Process, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("runner: no command")
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	setProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readLines(stdout, Stdout, onLine)
	}()
	go func() {
		defer wg.Done()
		readLines(stderr, Stderr, onLine)
	}()
	go func() {
		wg.Wait()
		err := cmd.Wait()
		p.mu.Lock()
		p.waited = true
		p.mu.Unlock()
		p.once.Do(func() { onExit(err) })
	}()
	return p, nil
}

// Stop kills the process and its children. Idempotent. onExit still runs
// once, after the output has drained, as cmd.Wait does that.
func (p *Process) Stop() {
	p.mu.Lock()
	if p.stopped || p.waited {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()
	killProcessGroup(p.cmd)
}

func readLines(r io.Reader, stream Stream, emit func(Line)) {
	br := bufio.NewReader(r)
	for {
		s, err := br.ReadString('\n')
		if len(s) > 0 {
			emit(Line{Text: strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r"), Stream: stream})
		}
		if err != nil {
			return
		}
	}
}
