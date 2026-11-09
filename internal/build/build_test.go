package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func makeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
		want  Kind
	}{
		{"maven", []string{"pom.xml"}, KindMaven},
		{"gradle", []string{"build.gradle"}, KindGradle},
		{"gradle kts", []string{"build.gradle.kts"}, KindGradle},
		{"maven wins", []string{"pom.xml", "build.gradle", "build.gradle.kts"}, KindMaven},
		{"none", nil, KindNone},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range c.files {
				write(t, root, f, "")
			}
			p, err := Detect(root)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kind != c.want || p.Root != root {
				t.Errorf("Detect(%q) = %+v, want kind %v", root, p, c.want)
			}
		})
	}
}

func TestMavenTasks(t *testing.T) {
	const plain = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <build>
    <plugins>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-compiler-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>`
	const spring = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <build>
    <pluginManagement>
      <plugins>
        <plugin>
          <groupId>org.springframework.boot</groupId>
          <artifactId>spring-boot-maven-plugin</artifactId>
        </plugin>
      </plugins>
    </pluginManagement>
  </build>
</project>`
	const springPlugins = `<project>
  <build>
    <plugins>
      <plugin>
        <artifactId>spring-boot-maven-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>`

	phases := []string{"clean", "validate", "compile", "test", "package", "verify", "install", "deploy"}
	for _, c := range []struct {
		name   string
		pom    string
		spring bool
	}{
		{"plain", plain, false},
		{"spring in pluginManagement", spring, true},
		{"spring in plugins", springPlugins, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "pom.xml", c.pom)
			tasks, err := Project{Root: root, Kind: KindMaven}.Tasks(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), phases...)
			if c.spring {
				want = append(want, "spring-boot:run")
			}
			if len(tasks) != len(want) {
				t.Fatalf("tasks %+v, want %v", tasks, want)
			}
			for i, name := range want {
				if tasks[i].Name != name || tasks[i].Description == "" {
					t.Errorf("task %d = %+v, want %q with a description", i, tasks[i], name)
				}
			}
			if c.spring && tasks[len(tasks)-1].Description != "Run the Spring Boot application" {
				t.Errorf("spring-boot:run description %q", tasks[len(tasks)-1].Description)
			}
		})
	}
}

func TestMavenMalformedPom(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pom.xml", "<project><build><plugins>")
	tasks, err := Project{Root: root, Kind: KindMaven}.Tasks(context.Background())
	if err == nil {
		t.Fatal("malformed pom parsed without an error")
	}
	if len(tasks) != len(lifecycleTasks) {
		t.Errorf("tasks %+v, want the lifecycle phases beside the error", tasks)
	}
}

func TestMavenMissingPom(t *testing.T) {
	tasks, err := Project{Root: t.TempDir(), Kind: KindMaven}.Tasks(context.Background())
	if err == nil {
		t.Fatal("missing pom read without an error")
	}
	if len(tasks) != len(lifecycleTasks) {
		t.Errorf("tasks %+v, want the lifecycle phases beside the error", tasks)
	}
}

const gradleOutput = `> Task :tasks

------------------------------------------------------------
Tasks runnable from root project 'demo'
------------------------------------------------------------

Build tasks
-----------
assemble - Assembles the outputs of this project.
build - Assembles and tests this project.
buildDependents - Assembles and tests this project and all projects that depend on it.

Build Setup tasks
-----------------
init - Initializes a new Gradle build.
wrapper - Generates Gradle wrapper files.

Documentation tasks
-------------------
javadoc - Generates Javadoc API documentation for the main source code.

Help tasks
----------
buildEnvironment - Displays all buildscript dependencies declared in root project 'demo'.
dependencies - Displays all dependencies declared in root project 'demo'.
help - Displays a help message.
tasks - Displays the tasks runnable from root project 'demo'.

Other tasks
-----------
cleanIdea - Cleans IDEA project files (if any).
prepareKotlinBuildScriptModel - Prepares Kotlin build script model.

All tasks run from the root project
-----------------------------------
app:tasks - Displays the tasks runnable from project ':app'.
app:build - Assembles the project ':app'.
build - A duplicate line keeps its first description.

Rules
-----
Pattern: clean<TaskName>: Cleans the output files of a task.

    --all     Shows additional tasks and their descriptions.
weird(task) - Has a parenthesis, so it is not a task name.

To see all tasks and more detail, run gradle tasks --all

BUILD SUCCESSFUL in 2s
8 actionable tasks: 1 executed, 7 up-to-date
`

func TestParseGradleTasks(t *testing.T) {
	want := []Task{
		{"assemble", "Assembles the outputs of this project."},
		{"build", "Assembles and tests this project."},
		{"buildDependents", "Assembles and tests this project and all projects that depend on it."},
		{"init", "Initializes a new Gradle build."},
		{"wrapper", "Generates Gradle wrapper files."},
		{"javadoc", "Generates Javadoc API documentation for the main source code."},
		{"buildEnvironment", "Displays all buildscript dependencies declared in root project 'demo'."},
		{"dependencies", "Displays all dependencies declared in root project 'demo'."},
		{"help", "Displays a help message."},
		{"tasks", "Displays the tasks runnable from root project 'demo'."},
		{"cleanIdea", "Cleans IDEA project files (if any)."},
		{"prepareKotlinBuildScriptModel", "Prepares Kotlin build script model."},
		{"app:tasks", "Displays the tasks runnable from project ':app'."},
		{"app:build", "Assembles the project ':app'."},
	}
	got := parseGradleTasks(gradleOutput)
	if len(got) != len(want) {
		t.Fatalf("parsed %d tasks %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := parseGradleTasks(""); len(got) != 0 {
		t.Errorf("empty output parsed %+v", got)
	}
}

func TestCommandWrapper(t *testing.T) {
	for _, c := range []struct {
		kind    Kind
		wrapper string
	}{
		{KindMaven, "mvnw"},
		{KindGradle, "gradlew"},
	} {
		t.Run(c.wrapper, func(t *testing.T) {
			root := t.TempDir()
			makeExecutable(t, write(t, root, c.wrapper, "#!/bin/sh\n"))
			got, err := Project{Root: root, Kind: c.kind}.Command("test", "--info")
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(root, c.wrapper)
			if len(got) != 3 || got[0] != want || got[1] != "test" || got[2] != "--info" {
				t.Errorf("Command = %q, want %q test --info", got, want)
			}
		})
	}
}

func TestCommandLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	for _, c := range []struct {
		kind    Kind
		wrapper string
		tool    string
	}{
		{KindMaven, "mvnw", "mvn"},
		{KindGradle, "gradlew", "gradle"},
	} {
		t.Run(c.tool, func(t *testing.T) {
			bin := t.TempDir()
			tool := filepath.Join(bin, c.tool)
			makeExecutable(t, write(t, bin, c.tool, "#!/bin/sh\n"))
			t.Setenv("PATH", bin)

			// A wrapper without an executable bit is not used.
			root := t.TempDir()
			write(t, root, c.wrapper, "#!/bin/sh\n")
			got, err := Project{Root: root, Kind: c.kind}.Command("package")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0] != tool || got[1] != "package" {
				t.Errorf("Command = %q, want %q package", got, tool)
			}
		})
	}
}

func TestCommandWrapperWinsOverPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	bin := t.TempDir()
	makeExecutable(t, write(t, bin, "mvn", "#!/bin/sh\n"))
	t.Setenv("PATH", bin)
	root := t.TempDir()
	makeExecutable(t, write(t, root, "mvnw", "#!/bin/sh\n"))
	got, err := Project{Root: root, Kind: KindMaven}.Command("clean")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "mvnw"); got[0] != want {
		t.Errorf("Command = %q, want the wrapper %q", got, want)
	}
}

func TestKindNone(t *testing.T) {
	p := Project{Root: t.TempDir(), Kind: KindNone}
	if _, err := p.Tasks(context.Background()); err == nil {
		t.Error("Tasks of no build had no error")
	}
	if _, err := p.Command("test"); err == nil {
		t.Error("Command of no build had no error")
	}
}
