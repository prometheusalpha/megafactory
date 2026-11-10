package app

import (
	"os"
	"path/filepath"
	"testing"
)

// sampleProject writes a small Spring Boot project to a temporary folder
// named spring-demo, and returns its path.
func sampleProject(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>demo</artifactId>
  <version>0.0.1-SNAPSHOT</version>
</project>
`,
		"README.md": "# demo\n\nA sample Spring Boot project.\n\n- `mvn spring-boot:run`\n",
		"src/main/java/com/example/demo/DemoApplication.java": `package com.example.demo;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

@SpringBootApplication
public class DemoApplication {

    public static void main(String[] args) {
        SpringApplication.run(DemoApplication.class, args);
    }
}
`,
		"src/main/java/com/example/demo/HelloController.java": `package com.example.demo;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class HelloController {

    @GetMapping("/hello")
    public String hello() {
        return "Hello, megafactory!";
    }
}
`,
		"src/main/resources/application.properties": "server.port=8080\nspring.application.name=demo\n",
		"src/main/resources/config.json":            "{\n  \"feature\": true,\n  \"limit\": 10\n}\n",
	}
	dir := filepath.Join(t.TempDir(), "spring-demo")
	for path, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
