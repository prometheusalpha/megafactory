package runcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeResolver struct {
	build BuildSystem
	java  string
}

func (f fakeResolver) Java(string) (string, error) { return f.java, nil }
func (f fakeResolver) Classpath(string) (string, []string, []string, error) {
	return "demo", []string{"/mods"}, []string{"/classes", "/lib/a.jar"}, nil
}
func (f fakeResolver) Wrapper(tool string) ([]string, error) {
	return []string{"/project/" + tool + "w", "-q"}, nil
}
func (f fakeResolver) ProjectRoot() string { return "/project" }
func (f fakeResolver) Build() BuildSystem  { return f.build }

func TestResolveBuildsEachKind(t *testing.T) {
	r := fakeResolver{build: BuildMaven, java: "/jdk/bin/java"}
	sep := string(os.PathListSeparator)
	c, err := Resolve(Config{ID: "1", Name: "App", Kind: Application,
		MainClass: "com.example.Demo", VMOptions: "-Xmx1g", ProgramArgs: "--debug x"}, r)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/jdk/bin/java", "-Xmx1g", "-p", "/mods", "-cp",
		strings.Join([]string{"/classes", "/lib/a.jar"}, sep), "com.example.Demo", "--debug", "x"}
	if strings.Join(c.Argv, " ") != strings.Join(want, " ") {
		t.Errorf("application argv %v", c.Argv)
	}
	if c.Dir != "/project" {
		t.Errorf("dir %q", c.Dir)
	}

	c, err = Resolve(Config{ID: "2", Name: "Boot", Kind: SpringBoot,
		MainClass: "com.example.App", Profiles: "dev,local"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if c.Argv[1] != "-Dspring.profiles.active=dev,local" {
		t.Errorf("spring profiles missing: %v", c.Argv)
	}

	c, err = Resolve(Config{ID: "3", Name: "Build", Kind: Maven, Goals: "clean install"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Argv, " ") != "/project/mvnw -q clean install" {
		t.Errorf("maven argv %v", c.Argv)
	}

	c, err = Resolve(Config{ID: "4", Name: "Sh", Kind: Shell, Command: "echo hi", WorkingDir: "sub", Env: "A=1\nB=2"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Argv, " ") != "/bin/sh -c echo hi" || c.Dir != "/project/sub" {
		t.Errorf("shell argv %v dir %q", c.Argv, c.Dir)
	}
	if len(c.Env) != 2 || c.Env[0] != "A=1" || c.Env[1] != "B=2" {
		t.Errorf("env %v", c.Env)
	}
}

func TestResolveRejectsMismatchedBuild(t *testing.T) {
	r := fakeResolver{build: BuildGradle}
	if _, err := Resolve(Config{ID: "1", Name: "M", Kind: Maven, Goals: "package"}, r); err == nil {
		t.Error("maven config on a gradle project resolved")
	}
	r.build = BuildNone
	if _, err := Resolve(Config{ID: "2", Name: "G", Kind: Gradle, Tasks: "build"}, r); err == nil {
		t.Error("gradle config on a project with no build resolved")
	}
}

func TestValidateRejectsIncompleteConfigs(t *testing.T) {
	cases := []Config{
		{Kind: Application, Name: "App"},
		{ID: "1", Kind: Maven, Name: "M"},
		{ID: "1", Kind: Gradle, Name: "G"},
		{ID: "1", Kind: Shell, Name: "S"},
		{ID: "1", Kind: "other", Name: "X"},
		{ID: "1", Kind: Application},
	}
	for _, c := range cases {
		if c.Validate() == nil {
			t.Errorf("%+v passed", c)
		}
	}
}

func TestStoreRoundTripAndCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".megafactory", "run.json")
	s, err := Load(path)
	if err != nil || len(s.All()) != 0 {
		t.Fatalf("missing file: %v %v", err, s.All())
	}
	app, err := s.Upsert(Config{Name: "App", Kind: Application, MainClass: "A"})
	if err != nil {
		t.Fatal(err)
	}
	s.Select(app.ID)
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	// A corrupt temp file must not truncate the saved one.
	if err := os.WriteFile(path+".tmp", []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := back.ByID(app.ID)
	if !ok || got.MainClass != "A" || back.Selected() != app.ID {
		t.Errorf("round trip: %v %v selected %q", got, ok, back.Selected())
	}
	dup, err := back.Duplicate(app.ID)
	if err != nil || dup.Name != "App (copy)" || dup.ID == app.ID {
		t.Errorf("duplicate: %v %v", dup, err)
	}
	back.Delete(app.ID)
	if _, ok := back.ByID(app.ID); ok {
		t.Error("deleted config still there")
	}
	if back.Selected() != dup.ID {
		t.Errorf("selection after delete %q", back.Selected())
	}
}

func TestLoadToleratesUnknownFieldsAndDropsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	data := `{"configs":[{"id":"1","name":"App","kind":"application","mainClass":"A","unknown":42},
	 {"id":"2","name":"Bad","kind":"maven"}],"selected":"1","extra":true}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 1 || s.All()[0].MainClass != "A" || s.Selected() != "1" {
		t.Errorf("loaded %v selected %q", s.All(), s.Selected())
	}
}

func TestSpecsCoverEveryKindWithFields(t *testing.T) {
	specs := Specs()
	if len(specs) != 5 {
		t.Fatalf("specs %d", len(specs))
	}
	for _, sp := range specs {
		if len(sp.Fields) == 0 || sp.Label == "" {
			t.Errorf("empty spec %+v", sp)
		}
		if _, ok := table[sp.Kind]; !ok {
			t.Errorf("spec kind %q not in table", sp.Kind)
		}
	}
	spring := table[SpringBoot].fields()
	if spring[len(spring)-1].Label != "Active profiles" {
		t.Errorf("spring boot lost its profiles field: %v", spring)
	}
}

func TestUpsertAfterReloadDoesNotReuseDeletedID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	s, _ := Load(path)
	first, _ := s.Upsert(Config{Name: "One", Kind: Shell, Command: "echo 1"})
	second, _ := s.Upsert(Config{Name: "Two", Kind: Shell, Command: "echo 2"})
	third, _ := s.Upsert(Config{Name: "Three", Kind: Shell, Command: "echo 3"})
	s.Delete(second.ID)
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path)
	added, err := back.Upsert(Config{Name: "Four", Kind: Shell, Command: "echo 4"})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == first.ID || added.ID == third.ID {
		t.Errorf("new config reused %q", added.ID)
	}
	if len(back.All()) != 3 {
		t.Errorf("configs %v", back.All())
	}
	for _, id := range []string{first.ID, third.ID, added.ID} {
		if _, ok := back.ByID(id); !ok {
			t.Errorf("lost %q", id)
		}
	}
}
