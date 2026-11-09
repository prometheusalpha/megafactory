package build

import (
	"encoding/xml"
	"os"
	"path/filepath"
)

// lifecycleTasks are Maven's phases, in the order a build runs them.
var lifecycleTasks = []Task{
	{"clean", "Remove the build output"},
	{"validate", "Validate the project is correct and all needed information is available"},
	{"compile", "Compile the sources"},
	{"test", "Run the tests"},
	{"package", "Package the compiled code in its distributable format"},
	{"verify", "Run the checks on the packaged build"},
	{"install", "Install the package in the local repository"},
	{"deploy", "Deploy the package to the remote repository"},
}

// pom is the part of a project's pom.xml a build needs: the plugins that
// add tasks, under build and under build's pluginManagement. The tags
// carry no namespace, so they match Maven's whatever it declares.
type pom struct {
	Build struct {
		Plugins          pluginList `xml:"plugins"`
		PluginManagement struct {
			Plugins pluginList `xml:"plugins"`
		} `xml:"pluginManagement"`
	} `xml:"build"`
}

type pluginList struct {
	Plugin []struct {
		ArtifactID string `xml:"artifactId"`
	} `xml:"plugin"`
}

// mavenTasks returns the lifecycle phases, and spring-boot:run when the
// pom asks for the Spring Boot plugin. A pom that cannot be read or
// parsed leaves the phases in place: the build can still run them, and
// the error is for the UI to show.
func mavenTasks(root string) ([]Task, error) {
	tasks := append([]Task(nil), lifecycleTasks...)
	b, err := os.ReadFile(filepath.Join(root, "pom.xml"))
	if err != nil {
		return tasks, err
	}
	var p pom
	if err := xml.Unmarshal(b, &p); err != nil {
		return tasks, err
	}
	for _, plugins := range []pluginList{p.Build.Plugins, p.Build.PluginManagement.Plugins} {
		for _, plugin := range plugins.Plugin {
			if plugin.ArtifactID == "spring-boot-maven-plugin" {
				return append(tasks, Task{"spring-boot:run", "Run the Spring Boot application"}), nil
			}
		}
	}
	return tasks, nil
}
