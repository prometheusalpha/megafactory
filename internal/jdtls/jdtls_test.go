package jdtls

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"megafactory/internal/jdk"
	"megafactory/internal/lsp"
	"megafactory/internal/setup"
)

// TestMain lets this test binary stand in for java: the fake JDK's
// launcher runs it with FAKE_JDTLS set, and it serves the handshake.
func TestMain(m *testing.M) {
	if out := os.Getenv("FAKE_JDTLS"); out != "" {
		fakeServer(out)
		return
	}
	os.Exit(m.Run())
}

type fake struct{ out string }

func (f fake) Notify(method string, params json.RawMessage) {
	switch method {
	case "initialized":
		os.WriteFile(f.out+".initialized", nil, 0o644)
	case "exit":
		os.Exit(0)
	}
}

func (f fake) Request(method string, params json.RawMessage) (any, error) {
	if method == "initialize" {
		os.WriteFile(f.out+".params", params, 0o644)
		return map[string]any{"capabilities": map[string]any{"hoverProvider": true}}, nil
	}
	return nil, nil
}

func fakeServer(out string) {
	os.WriteFile(out+".args", []byte(strings.Join(os.Args[1:], "\n")), 0o644)
	c := lsp.NewConn(os.Stdin, os.Stdout, fake{out})
	<-c.Done()
}

// install lays out a servers folder as setup leaves it, and a JDK whose
// java is this test binary.
func install(t *testing.T) (cfg Config, out string) {
	t.Helper()
	servers := t.TempDir()
	home := setup.JDTLS.Dir(servers)
	os.MkdirAll(filepath.Join(home, "plugins"), 0o755)
	os.WriteFile(filepath.Join(home, "plugins", "org.eclipse.equinox.launcher_1.8.0.jar"), nil, 0o644)
	jdkHome := t.TempDir()
	os.MkdirAll(filepath.Join(jdkHome, "bin"), 0o755)
	exe, _ := os.Executable()
	script := "#!/bin/sh\nexec '" + exe + "' \"$@\"\n"
	os.WriteFile(filepath.Join(jdkHome, "bin", "java"), []byte(script), 0o755)
	out = filepath.Join(t.TempDir(), "fake")
	t.Setenv("FAKE_JDTLS", out)
	return Config{
		JDK:        jdk.JDK{Home: jdkHome, Version: "25", Major: 25},
		Runtimes:   []jdk.JDK{{Home: "/jdks/21", Version: "21.0.1", Major: 21}},
		Servers:    servers,
		Project:    t.TempDir(),
		Workspaces: filepath.Join(t.TempDir(), "workspaces"),
	}, out
}

func TestWorkspaceDir(t *testing.T) {
	a := WorkspaceDir("/w", "/code/shop")
	if a != WorkspaceDir("/w", "/code/shop/") {
		t.Error("a trailing slash makes another workspace")
	}
	if a == WorkspaceDir("/w", "/other/shop") {
		t.Error("projects with the same name share a workspace")
	}
	if !strings.HasPrefix(a, "/w/shop-") {
		t.Errorf("dir %s", a)
	}
}

func TestPlatformConfig(t *testing.T) {
	for _, c := range [][3]string{
		{"darwin", "arm64", "config_mac_arm"}, {"darwin", "amd64", "config_mac"},
		{"linux", "arm64", "config_linux_arm"}, {"linux", "amd64", "config_linux"},
		{"windows", "amd64", "config_win"},
	} {
		if got := platformConfig(c[0], c[1]); got != c[2] {
			t.Errorf("%s/%s: %s, want %s", c[0], c[1], got, c[2])
		}
	}
}

func TestCommandWithoutInstall(t *testing.T) {
	if _, err := Command(Config{Servers: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err %v", err)
	}
}

func TestStartHandshake(t *testing.T) {
	cfg, out := install(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(s.Capabilities) != `{"hoverProvider":true}` {
		t.Errorf("capabilities %s", s.Capabilities)
	}
	args, _ := os.ReadFile(out + ".args")
	got := strings.Split(string(args), "\n")
	data := WorkspaceDir(cfg.Workspaces, cfg.Project)
	if !slices.Contains(got, data) || !slices.Contains(got, "-data") {
		t.Errorf("args %q lack -data %s", got, data)
	}
	if fi, err := os.Stat(cfg.Workspaces); err != nil || !fi.IsDir() {
		t.Errorf("workspaces folder: %v", err)
	}
	var p struct {
		RootURI string `json:"rootUri"`
		Init    struct {
			Bundles  []string `json:"bundles"`
			Settings struct {
				Java struct {
					Configuration struct {
						Runtimes []struct{ Name, Path string } `json:"runtimes"`
					} `json:"configuration"`
				} `json:"java"`
			} `json:"settings"`
		} `json:"initializationOptions"`
	}
	raw, _ := os.ReadFile(out + ".params")
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.RootURI != URI(cfg.Project) || len(p.Init.Bundles) != 1 || !strings.HasSuffix(p.Init.Bundles[0], setup.JavaDebugJar) {
		t.Errorf("params %s", raw)
	}
	// A project of Java 21 is analysed against a JDK 21, not the 25 that runs jdtls.
	if rts := p.Init.Settings.Java.Configuration.Runtimes; len(rts) != 1 || rts[0].Name != "JavaSE-21" || rts[0].Path != "/jdks/21" {
		t.Errorf("runtimes %+v", rts)
	}
	// initialized is a notification: give it a moment to land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(out + ".initialized"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(out + ".initialized"); err != nil {
		t.Error("initialized was not sent")
	}
	s.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Error("connection still open after Close")
	}
}

func TestStartReportsServerThatDies(t *testing.T) {
	cfg, _ := install(t)
	os.WriteFile(filepath.Join(cfg.JDK.Home, "bin", "java"), []byte("#!/bin/sh\necho 'Unsupported class file' >&2\nexit 1\n"), 0o755)
	_, err := Start(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "Unsupported class file") {
		t.Errorf("err %v", err)
	}
}

// diagnostics collects what jdtls publishes, and answers its requests
// with nothing, as a client with no settings does.
type diagnostics struct{ ch chan json.RawMessage }

func (d diagnostics) Notify(method string, params json.RawMessage) {
	if method == "textDocument/publishDiagnostics" {
		select {
		case d.ch <- params:
		default:
		}
	}
}

func (d diagnostics) Request(method string, params json.RawMessage) (any, error) {
	if method == "workspace/configuration" {
		var p struct{ Items []any }
		json.Unmarshal(params, &p)
		return make([]any, len(p.Items)), nil
	}
	return nil, nil
}

// Run with MEGAFACTORY_JDTLS=1 (and JAVA_HOME at a JDK 25) to start the
// real jdtls, installed by the app, on a Maven project of Java 21: its
// records, sealed types and pattern switches must pass, and a Java 25
// feature, which the project's level 21 forbids, must not.
func TestRealJDTLSJava21(t *testing.T) {
	if os.Getenv("MEGAFACTORY_JDTLS") == "" {
		t.Skip("MEGAFACTORY_JDTLS not set")
	}
	j, err := jdk.System.Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtimes := jdk.System.Installed()
	t.Logf("jdtls on JDK %s, runtimes %+v", j.Version, runtimes)
	// jdtls names files by their real path, which a temp folder's link hides.
	project, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(project, "src/main/java/app"), 0o755)
	os.WriteFile(filepath.Join(project, "pom.xml"), []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>app</groupId><artifactId>app</artifactId><version>1</version>
  <properties><maven.compiler.release>21</maven.compiler.release></properties>
</project>`), 0o644)
	good := filepath.Join(project, "src/main/java/app/Good.java")
	os.WriteFile(good, []byte(`package app;
sealed interface Shape permits Circle, Square {}
record Circle(double r) implements Shape {}
record Square(double s) implements Shape {}
public class Good {
    static double area(Shape sh) {
        return switch (sh) {
            case Circle c -> Math.PI * c.r() * c.r();
            case Square q -> q.s() * q.s();
        };
    }
}
`), 0o644)
	bad := filepath.Join(project, "src/main/java/app/Bad.java")
	// Statements before super() are final in Java 25, which runs jdtls, and
	// an error in the project's level 21.
	os.WriteFile(bad, []byte(`package app;
public class Bad extends Object {
    Bad(int x) { if (x < 0) throw new IllegalArgumentException(); super(); }
}
`), 0o644)

	d := diagnostics{make(chan json.RawMessage, 64)}
	home, _ := os.UserHomeDir()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	s, err := Start(ctx, Config{
		JDK: j, Runtimes: runtimes, Handler: d,
		Servers:    filepath.Join(home, "Library/Application Support/megafactory/servers"),
		Project:    project,
		Workspaces: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, f := range []string{good, bad} {
		src, _ := os.ReadFile(f)
		s.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": URI(f), "languageId": "java", "version": 1, "text": string(src)}})
	}
	// The last word of each file counts: jdtls first diagnoses with the
	// default level, then again once the import has made the project's known.
	errs := map[string][]string{}
	for quiet := time.NewTimer(150 * time.Second); ; {
		select {
		case raw := <-d.ch:
			var p struct {
				URI         string `json:"uri"`
				Diagnostics []struct {
					Severity int
					Message  string
				}
			}
			json.Unmarshal(raw, &p)
			errs[p.URI] = nil
			for _, dg := range p.Diagnostics {
				if dg.Severity == 1 {
					errs[p.URI] = append(errs[p.URI], dg.Message)
				}
			}
			t.Logf("%s: %q", filepath.Base(p.URI), errs[p.URI])
			quiet.Reset(25 * time.Second)
			continue
		case <-quiet.C:
		}
		break
	}
	if len(errs[URI(bad)]) == 0 {
		t.Errorf("a Java 22 feature passes in a project of level 21")
	}
	if len(errs[URI(good)]) != 0 {
		t.Errorf("Java 21 code has errors: %q", errs[URI(good)])
	}
}

func TestCommandLombokAgent(t *testing.T) {
	cfg, _ := install(t)
	cmd, err := Command(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range cmd.Args {
		if strings.HasPrefix(a, "-javaagent") {
			t.Errorf("agent %s without Lombok", a)
		}
	}
	cfg.Lombok = "/servers/lombok.jar"
	cmd, _ = Command(cfg)
	i := slices.Index(cmd.Args, "-javaagent:/servers/lombok.jar")
	// The JVM reads its options up to the jar.
	if i < 0 || i > slices.Index(cmd.Args, "-jar") {
		t.Errorf("args %q", cmd.Args)
	}
}

func TestUsesLombok(t *testing.T) {
	write := func(root, name, body string) {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	plain := t.TempDir()
	write(plain, "pom.xml", "<artifactId>spring-boot</artifactId>")
	// A target folder's copy of a pom is not the project's.
	write(plain, "target/pom.xml", "lombok")
	if UsesLombok(plain) {
		t.Error("Lombok in a project without it")
	}
	top := t.TempDir()
	write(top, "build.gradle", "compileOnly 'org.projectlombok:lombok:1.18'")
	if !UsesLombok(top) {
		t.Error("Lombok in build.gradle missed")
	}
	module := t.TempDir()
	write(module, "pom.xml", "<modules/>")
	write(module, "svc/api/pom.xml", "<artifactId>Lombok</artifactId>")
	if !UsesLombok(module) {
		t.Error("Lombok in a module missed")
	}
}
