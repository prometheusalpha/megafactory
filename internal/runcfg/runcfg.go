// Package runcfg owns the run-configuration model, its persistence in
// .megafactory/run.json, and the one translation from a config to a command
// the runner can spawn. All knowledge that varies by kind lives in table.
package runcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Kind is the discriminator in Config and the key into table.
type Kind string

const (
	Application Kind = "application"
	SpringBoot  Kind = "springBoot"
	Maven       Kind = "maven"
	Gradle      Kind = "gradle"
	Shell       Kind = "shell"
)

// BuildSystem is what the project's build files declare, supplied by main.
// A Maven or Gradle config on the wrong build fails with a clear error.
type BuildSystem int

const (
	BuildNone BuildSystem = iota
	BuildMaven
	BuildGradle
)

// Config is one run configuration. Fields irrelevant to a kind are ignored
// by its builder and hidden by its editor fields. The flat shape makes the
// on-disk JSON and the in-memory value identical.
type Config struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	MainClass   string `json:"mainClass,omitempty"`
	Module      string `json:"module,omitempty"`
	VMOptions   string `json:"vmOptions,omitempty"`
	ProgramArgs string `json:"programArgs,omitempty"`
	JDK         string `json:"jdk,omitempty"`
	Profiles    string `json:"profiles,omitempty"`
	Goals       string `json:"goals,omitempty"`
	Tasks       string `json:"tasks,omitempty"`
	Command     string `json:"command,omitempty"`
	Env         string `json:"env,omitempty"`
	WorkingDir  string `json:"workingDir,omitempty"`
}

// Command is the spawnable result of Resolve. Argv is exec-ready, Dir is
// absolute, and Env holds only the config's additions: the runner appends
// them to os.Environ itself.
type Command struct {
	Argv []string
	Dir  string
	Env  []string
}

// Resolver is the I/O port for facts Resolve cannot derive. package main
// implements it; its methods run off the UI thread inside host.run.
type Resolver interface {
	Java(jdk string) (string, error)
	Classpath(mainClass string) (projectName string, modules, classes []string, err error)
	Wrapper(tool string) ([]string, error)
	ProjectRoot() string
	Build() BuildSystem
}

// Field binds an editor input straight to a Config field.
type Field struct {
	Label string
	Ptr   func(*Config) *string
	Multi bool // a textarea instead of a line input
	Pick  bool // a main-class picker fed by javalang.ResolveMainClasses
}

// Spec is a kind's descriptor, derived from table.
type Spec struct {
	Kind   Kind
	Label  string
	Fields []Field
}

type builder func(Config, Resolver) ([]string, error)

// row is everything that varies by one kind. Adding a kind is one row; the
// dialog, the validator, the argv builder and the debugger all read from
// here.
type row struct {
	label    string
	build    builder
	validate func(Config) error
	fields   func() []Field
	// debug is set by the kinds a debug adapter launches itself: a Maven
	// goal or a shell command has no Java program to debug.
	debug func(Config, Resolver) (Debug, error)
}

var kindOrder = []Kind{Application, SpringBoot, Maven, Gradle, Shell}

var table = map[Kind]row{
	Application: {label: "Application", build: buildJava, validate: needMain, fields: appFields, debug: buildJavaDebug},
	SpringBoot:  {label: "Spring Boot", build: buildSpring, validate: needMain, fields: springFields, debug: buildSpringDebug},
	Maven:       {label: "Maven", build: buildMaven, validate: needGoals, fields: mavenFields},
	Gradle:      {label: "Gradle", build: buildGradle, validate: needTasks, fields: gradleFields},
	Shell:       {label: "Shell", build: buildShell, validate: needCmd, fields: shellFields},
}

// Specs returns all kinds in stable display order. It is the single list the
// add menu and the editor read.
func Specs() []Spec {
	out := make([]Spec, 0, len(kindOrder))
	for _, k := range kindOrder {
		r := table[k]
		out = append(out, Spec{Kind: k, Label: r.label, Fields: r.fields()})
	}
	return out
}

// Resolve is the one caller-facing call. It dispatches through table and
// derives Dir and Env, so no caller branches on Kind.
func Resolve(cfg Config, r Resolver) (Command, error) {
	if err := cfg.Validate(); err != nil {
		return Command{}, err
	}
	row, ok := table[cfg.Kind]
	if !ok {
		return Command{}, fmt.Errorf("unknown run configuration kind %q", cfg.Kind)
	}
	argv, err := row.build(cfg, r)
	if err != nil {
		return Command{}, err
	}
	return Command{Argv: argv, Dir: workDir(cfg, r), Env: envLines(cfg.Env)}, nil
}

// workDir is where a configuration runs: the project, or its own folder.
func workDir(cfg Config, r Resolver) string {
	root := r.ProjectRoot()
	if cfg.WorkingDir == "" {
		return root
	}
	if filepath.IsAbs(cfg.WorkingDir) {
		return cfg.WorkingDir
	}
	return filepath.Join(root, cfg.WorkingDir)
}

// Debug is what a debug adapter launches: the JVM's own settings instead of
// a command line, since java-debug runs java itself.
type Debug struct {
	MainClass   string
	ProjectName string
	Cwd         string
	Java        string
	VMArgs      string
	Args        string
	ModulePaths []string
	ClassPaths  []string
	Env         map[string]string
}

// DebugSpec is the debugger's counterpart of Resolve, dispatching through
// the same table.
func DebugSpec(cfg Config, r Resolver) (Debug, error) {
	if err := cfg.Validate(); err != nil {
		return Debug{}, err
	}
	row := table[cfg.Kind]
	if row.debug == nil {
		return Debug{}, fmt.Errorf("a %s configuration cannot be debugged", row.label)
	}
	return row.debug(cfg, r)
}

// Debuggable reports whether a kind is one a debug adapter launches, which
// the debugger offers and the toolbar shows.
func (k Kind) Debuggable() bool { return table[k].debug != nil }

// Validate is the single validation boundary, called by Load and Upsert.
func (c Config) Validate() error {
	if c.ID == "" || c.Name == "" {
		return errors.New("a run configuration needs an id and a name")
	}
	row, ok := table[c.Kind]
	if !ok {
		return fmt.Errorf("unknown run configuration kind %q", c.Kind)
	}
	return row.validate(c)
}

func javaCmd(cfg Config, r Resolver, extraVM string) ([]string, error) {
	java, err := r.Java(cfg.JDK)
	if err != nil {
		return nil, err
	}
	_, modules, classes, err := r.Classpath(cfg.MainClass)
	if err != nil {
		return nil, err
	}
	argv := append([]string{java}, strings.Fields(cfg.VMOptions)...)
	if extraVM != "" {
		argv = append(argv, extraVM)
	}
	switch {
	case cfg.Module != "":
		if len(modules) == 0 {
			return nil, errors.New("the module has no module path")
		}
		argv = append(argv, "-p", strings.Join(modules, string(os.PathListSeparator)),
			"-m", cfg.Module+"/"+cfg.MainClass)
	case len(classes) > 0:
		if len(modules) > 0 {
			argv = append(argv, "-p", strings.Join(modules, string(os.PathListSeparator)))
		}
		argv = append(argv, "-cp", strings.Join(classes, string(os.PathListSeparator)), cfg.MainClass)
	default:
		return nil, errors.New("no classpath for " + cfg.MainClass)
	}
	return append(argv, strings.Fields(cfg.ProgramArgs)...), nil
}

// javaDebug is what java-debug launches with: the same class, paths and
// VM options javaCmd builds a command line from.
func javaDebug(cfg Config, r Resolver, extraVM string) (Debug, error) {
	java, err := r.Java(cfg.JDK)
	if err != nil {
		return Debug{}, err
	}
	project, modules, classes, err := r.Classpath(cfg.MainClass)
	if err != nil {
		return Debug{}, err
	}
	if cfg.Module == "" && len(classes) == 0 {
		return Debug{}, errors.New("no classpath for " + cfg.MainClass)
	}
	vm := []string{}
	if cfg.VMOptions != "" {
		vm = append(vm, cfg.VMOptions)
	}
	if extraVM != "" {
		vm = append(vm, extraVM)
	}
	return Debug{
		MainClass:   cfg.MainClass,
		ProjectName: project,
		Cwd:         workDir(cfg, r),
		Java:        java,
		VMArgs:      strings.Join(vm, " "),
		Args:        cfg.ProgramArgs,
		ModulePaths: modules,
		ClassPaths:  classes,
		Env:         envMap(cfg.Env),
	}, nil
}

func buildJava(c Config, r Resolver) ([]string, error) { return javaCmd(c, r, "") }

func buildSpring(c Config, r Resolver) ([]string, error) {
	return javaCmd(c, r, springVM(c))
}

func buildJavaDebug(c Config, r Resolver) (Debug, error) { return javaDebug(c, r, "") }

func buildSpringDebug(c Config, r Resolver) (Debug, error) {
	return javaDebug(c, r, springVM(c))
}

// springVM is the property that picks the application's profiles.
func springVM(c Config) string {
	if c.Profiles == "" {
		return ""
	}
	return "-Dspring.profiles.active=" + c.Profiles
}

func buildMaven(c Config, r Resolver) ([]string, error) {
	if r.Build() != BuildMaven {
		return nil, errors.New("a Maven goal needs a pom.xml")
	}
	w, err := r.Wrapper("mvn")
	if err != nil {
		return nil, err
	}
	return append(w, strings.Fields(c.Goals)...), nil
}

func buildGradle(c Config, r Resolver) ([]string, error) {
	if r.Build() != BuildGradle {
		return nil, errors.New("a Gradle task needs build.gradle or build.gradle.kts")
	}
	w, err := r.Wrapper("gradle")
	if err != nil {
		return nil, err
	}
	return append(w, strings.Fields(c.Tasks)...), nil
}
func buildShell(c Config, _ Resolver) ([]string, error) {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", c.Command}, nil
	}
	return []string{"/bin/sh", "-c", c.Command}, nil
}

func envLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// envMap is envLines as an adapter's environment: a line without a name is
// not one a process could have either, so it is left out.
func envMap(s string) map[string]string {
	out := map[string]string{}
	for _, l := range envLines(s) {
		if k, v, ok := strings.Cut(l, "="); ok && k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appFields() []Field {
	return []Field{
		{Label: "Main class", Ptr: func(c *Config) *string { return &c.MainClass }, Pick: true},
		{Label: "Module", Ptr: func(c *Config) *string { return &c.Module }},
		{Label: "Program arguments", Ptr: func(c *Config) *string { return &c.ProgramArgs }, Multi: true},
		{Label: "VM options", Ptr: func(c *Config) *string { return &c.VMOptions }, Multi: true},
		{Label: "Environment", Ptr: func(c *Config) *string { return &c.Env }, Multi: true},
		{Label: "Working directory", Ptr: func(c *Config) *string { return &c.WorkingDir }},
		{Label: "JDK", Ptr: func(c *Config) *string { return &c.JDK }},
	}
}

func springFields() []Field {
	return append(appFields(), Field{Label: "Active profiles", Ptr: func(c *Config) *string { return &c.Profiles }})
}

func mavenFields() []Field {
	return []Field{
		{Label: "Goals", Ptr: func(c *Config) *string { return &c.Goals }, Multi: true},
		{Label: "Environment", Ptr: func(c *Config) *string { return &c.Env }, Multi: true},
		{Label: "Working directory", Ptr: func(c *Config) *string { return &c.WorkingDir }},
	}
}

func gradleFields() []Field {
	return []Field{
		{Label: "Tasks", Ptr: func(c *Config) *string { return &c.Tasks }, Multi: true},
		{Label: "Environment", Ptr: func(c *Config) *string { return &c.Env }, Multi: true},
		{Label: "Working directory", Ptr: func(c *Config) *string { return &c.WorkingDir }},
	}
}

func shellFields() []Field {
	return []Field{
		{Label: "Command", Ptr: func(c *Config) *string { return &c.Command }, Multi: true},
		{Label: "Environment", Ptr: func(c *Config) *string { return &c.Env }, Multi: true},
		{Label: "Working directory", Ptr: func(c *Config) *string { return &c.WorkingDir }},
	}
}

func needMain(c Config) error {
	if c.MainClass == "" {
		return errors.New("a main class is required")
	}
	return nil
}

func needGoals(c Config) error {
	if c.Goals == "" {
		return errors.New("Maven goals are required")
	}
	return nil
}

func needTasks(c Config) error {
	if c.Tasks == "" {
		return errors.New("Gradle tasks are required")
	}
	return nil
}

func needCmd(c Config) error {
	if c.Command == "" {
		return errors.New("a shell command is required")
	}
	return nil
}

// Store owns the project's configs. Its slice is unexported, so every Config
// inside has passed Validate.
type Store struct {
	configs  []Config
	selected string
	next     int
}

type fileStore struct {
	Configs  []Config `json:"configs"`
	Selected string   `json:"selected"`
}

// Load reads path. A missing file yields an empty Store and no error. A
// config with no id gets one, and configs that fail Validate are dropped, so
// a hand-edited file never yields an invalid Store.
func Load(path string) (*Store, error) {
	s := &Store{next: 1}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var f fileStore
	if err := json.Unmarshal(data, &f); err != nil {
		return s, err
	}
	for i, c := range f.Configs {
		if c.ID == "" {
			c.ID = fmt.Sprintf("cfg-%d", i+1)
		}
		if c.Validate() != nil {
			continue
		}
		s.configs = append(s.configs, c)
	}
	s.selected = f.Selected
	s.next = len(f.Configs) + 1
	return s, nil
}

// Save is atomic: a failed write leaves the previous file intact.
func (s *Store) Save(path string) error {
	data, err := json.MarshalIndent(fileStore{Configs: s.configs, Selected: s.selected}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) All() []Config { return append([]Config(nil), s.configs...) }

func (s *Store) ByID(id string) (Config, bool) {
	for _, c := range s.configs {
		if c.ID == id {
			return c, true
		}
	}
	return Config{}, false
}

func (s *Store) Selected() string { return s.selected }

func (s *Store) Select(id string) { s.selected = id }

func (s *Store) Upsert(c Config) (Config, error) {
	if c.ID == "" {
		// A reload derives next from the file's length, so a deleted id
		// can be reused. Keep incrementing until the id is free.
		for {
			c.ID = fmt.Sprintf("cfg-%d", s.next)
			s.next++
			if _, taken := s.ByID(c.ID); !taken {
				break
			}
		}
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	for i, old := range s.configs {
		if old.ID == c.ID {
			s.configs[i] = c
			return c, nil
		}
	}
	s.configs = append(s.configs, c)
	return c, nil
}

func (s *Store) Delete(id string) {
	s.configs = deleteID(s.configs, id)
	if s.selected == id {
		s.selected = ""
		if len(s.configs) > 0 {
			s.selected = s.configs[0].ID
		}
	}
}

func (s *Store) Duplicate(id string) (Config, error) {
	c, ok := s.ByID(id)
	if !ok {
		return Config{}, fmt.Errorf("no run configuration %q", id)
	}
	c.ID, c.Name = "", c.Name+" (copy)"
	return s.Upsert(c)
}

func deleteID(cs []Config, id string) []Config {
	out := make([]Config, 0, len(cs))
	for _, c := range cs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return out
}
