package javalang

import "testing"

func TestRunnableReportsLineAndFQCN(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		src  string
		line int
		fqcn string
		ok   bool
	}{
		{
			name: "plain main",
			rel:  "src/main/java/com/example/Demo.java",
			src:  "package com.example;\n\npublic class Demo {\n  public static void main(String[] args) {}\n}\n",
			line: 3, fqcn: "com.example.Demo", ok: true,
		},
		{
			name: "spring boot",
			rel:  "src/main/java/com/example/App.java",
			src:  "package com.example;\n\n@SpringBootApplication\npublic class App {}\n",
			line: 2, fqcn: "com.example.App", ok: true,
		},
		{
			name: "prefixed annotation",
			rel:  "App.java",
			src:  "@org.springframework.boot.autoconfigure.SpringBootApplication\npublic class App {}\n",
			line: 0, fqcn: "App", ok: true,
		},
		{
			name: "no package",
			rel:  "Demo.java",
			src:  "class Demo { public static void main(String[] a) {} }\n",
			line: 0, fqcn: "Demo", ok: true,
		},
		{
			name: "commented out main",
			rel:  "Demo.java",
			src:  "// public static void main(String[] a) {}\nclass Demo {}\n",
			ok:   false,
		},
		{
			name: "block commented annotation",
			rel:  "App.java",
			src:  "/* @SpringBootApplication */\nclass App {}\n",
			ok:   false,
		},
		{
			name: "marker after block comment ends",
			rel:  "Demo.java",
			src:  "/* a\n b */\nclass Demo { public static void main(String[] a) {} }\n",
			line: 2, fqcn: "Demo", ok: true,
		},
		{
			name: "not java",
			rel:  "README.md",
			src:  "public static void main(String[] a) {}\n",
			ok:   false,
		},
		{
			name: "no marker",
			rel:  "Demo.java",
			src:  "package com.example;\nclass Demo {}\n",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, fqcn, ok := Runnable(tc.rel, tc.src)
			if ok != tc.ok || line != tc.line || fqcn != tc.fqcn {
				t.Errorf("got line %d fqcn %q ok %v, want line %d fqcn %q ok %v",
					line, fqcn, ok, tc.line, tc.fqcn, tc.ok)
			}
		})
	}
}

func TestSpringBootIgnoresComments(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"@SpringBootApplication\nclass App {}", true},
		{"@org.springframework.boot.autoconfigure.SpringBootApplication\nclass App {}", true},
		{"// @SpringBootApplication\nclass App {}", false},
		{"/* @SpringBootApplication */\nclass App {}", false},
		{"class App {}", false},
	}
	for _, tc := range cases {
		if got := SpringBoot("App.java", tc.src); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.src, got, tc.want)
		}
	}
	if SpringBoot("App.md", "@SpringBootApplication") {
		t.Error("a markdown file is not a Spring Boot class")
	}
}
