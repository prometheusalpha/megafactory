// Package jdtls starts the Eclipse Java language server for a project
// and brings its connection through the LSP handshake.
package jdtls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"megafactory/internal/jdk"
	"megafactory/internal/lsp"
	"megafactory/internal/setup"
)

// Config says what to start.
type Config struct {
	// JDK runs jdtls. Runtimes are the JDKs a project may target, one per
	// Java version: a project of level 21 is analysed against the JDK 21
	// among them, whatever JDK is running jdtls.
	JDK      jdk.JDK
	Runtimes []jdk.JDK
	// Servers is the folder setup installs jdtls and java-debug under.
	Servers string
	Project string
	// Workspaces holds the data folder of each project's jdtls.
	Workspaces string
	Handler    lsp.Handler
}

// Server is a running jdtls, past its handshake.
type Server struct {
	*lsp.Conn
	cmd *exec.Cmd
	in  io.Closer
	// Capabilities are what the server said it can do, as it sent them.
	Capabilities []byte
}

// WorkspaceDir is the data folder jdtls keeps the project's index and
// metadata in: its own per project, named by the project's path, as two
// projects sharing one corrupt each other's.
func WorkspaceDir(workspaces, project string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(project)))
	return filepath.Join(workspaces, filepath.Base(project)+"-"+hex.EncodeToString(sum[:])[:12])
}

// platformConfig is the folder of jdtls's configuration for this system.
func platformConfig(goos, goarch string) string {
	name := map[string]string{"darwin": "config_mac", "linux": "config_linux", "windows": "config_win"}[goos]
	if name == "" {
		name = "config_linux"
	}
	if goarch == "arm64" && goos != "windows" {
		name += "_arm"
	}
	return name
}

// Command is how to launch jdtls from its folder: the Equinox launcher
// on the JDK's java, as jdtls's own script does.
func Command(cfg Config) (*exec.Cmd, error) {
	home := setup.JDTLS.Dir(cfg.Servers)
	jars, _ := filepath.Glob(filepath.Join(home, "plugins", "org.eclipse.equinox.launcher_*.jar"))
	if len(jars) == 0 {
		return nil, fmt.Errorf("jdtls is not installed in %s", home)
	}
	conf := platformConfig(runtime.GOOS, runtime.GOARCH)
	// jdtls writes into its configuration folder: the install's, which is the
	// user's own, as setup makes it, not a system one.
	cmd := exec.Command(cfg.JDK.Java(),
		"-Declipse.application=org.eclipse.jdt.ls.core.id1",
		"-Dosgi.bundles.defaultStartLevel=4",
		"-Declipse.product=org.eclipse.jdt.ls.core.product",
		"-Dlog.level=WARNING",
		"-Xmx1G",
		"--add-modules=ALL-SYSTEM",
		"--add-opens", "java.base/java.util=ALL-UNNAMED",
		"--add-opens", "java.base/java.lang=ALL-UNNAMED",
		"-jar", jars[0],
		"-configuration", filepath.Join(home, conf),
		"-data", WorkspaceDir(cfg.Workspaces, cfg.Project),
	)
	cmd.Dir = cfg.Project
	return cmd, nil
}

// Bundles are the jars jdtls loads as plugins: java-debug's.
func Bundles(servers string) []string {
	return []string{filepath.Join(setup.JavaDebug.Dir(servers), setup.JavaDebugJar)}
}

// URI is the file URI of a path.
func URI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// InitializeParams is the handshake's request for a project.
func InitializeParams(project string, pid int, bundles []string, runtimes []jdk.JDK) map[string]any {
	uri := URI(project)
	var rts []map[string]any
	for _, r := range runtimes {
		rts = append(rts, map[string]any{"name": jdk.RuntimeName(r.Major), "path": r.Home})
	}
	return map[string]any{
		"processId":  pid,
		"clientInfo": map[string]string{"name": "megafactory"},
		"rootUri":    uri,
		"workspaceFolders": []map[string]string{
			{"uri": uri, "name": filepath.Base(project)},
		},
		"initializationOptions": map[string]any{
			"bundles":          bundles,
			"workspaceFolders": []string{uri},
			"settings": map[string]any{
				"java": map[string]any{"configuration": map[string]any{"runtimes": rts}},
			},
			"extendedClientCapabilities": map[string]any{
				"progressReportProvider":          true,
				"classFileContentsSupport":        true,
				"executeClientCommandSupport":     true,
				"snippetEditSupport":              true,
				"actionableNotificationSupported": true,
			},
		},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
				"applyEdit":        true,
				// A build file changing is how the session asks jdtls to
				// re-import a project.
				"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true},
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": true},
				"completion": map[string]any{
					"completionItem": map[string]any{
						"snippetSupport":       true,
						"insertReplaceSupport": true,
						"documentationFormat":  []string{"markdown", "plaintext"},
					},
				},
				"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"definition":         map[string]any{"linkSupport": false},
				"publishDiagnostics": map[string]any{},
			},
			"window": map[string]any{"workDoneProgress": true},
		},
	}
}

// Start launches jdtls and completes the handshake: initialize, then
// initialized. ctx bounds the handshake only; Close stops the server.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	if err := os.MkdirAll(cfg.Workspaces, 0o755); err != nil {
		return nil, err
	}
	cmd, err := Command(cfg)
	if err != nil {
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr tail
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start jdtls: %w", err)
	}
	s := &Server{Conn: lsp.NewConn(out, in, cfg.Handler), cmd: cmd, in: in}

	type reply struct {
		Capabilities rawJSON `json:"capabilities"`
	}
	var res reply
	errc := make(chan error, 1)
	go func() {
		errc <- s.Call("initialize", InitializeParams(cfg.Project, os.Getpid(), Bundles(cfg.Servers), cfg.Runtimes), &res)
	}()
	select {
	case err = <-errc:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil {
		err = s.Notify("initialized", map[string]any{})
	}
	if err != nil {
		s.Close()
		if msg := stderr.String(); msg != "" && !errors.Is(err, context.Canceled) {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return nil, fmt.Errorf("jdtls initialize: %w", err)
	}
	s.Capabilities = res.Capabilities
	return s, nil
}

// Close asks jdtls to shut down, then ends it.
func (s *Server) Close() error {
	// Politely, if it still listens; a stuck server is killed below, after
	// a moment.
	select {
	case <-s.Done():
	default:
		bye := make(chan struct{})
		go func() {
			defer close(bye)
			s.Call("shutdown", nil, nil)
			s.Notify("exit", nil)
		}()
		select {
		case <-bye:
		case <-time.After(3 * time.Second):
		}
	}
	s.in.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	return s.cmd.Wait()
}

type rawJSON []byte

func (r *rawJSON) UnmarshalJSON(b []byte) error { *r = append((*r)[:0], b...); return nil }

// tail keeps the end of what jdtls prints on stderr, for the error a
// failed start reports.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}
