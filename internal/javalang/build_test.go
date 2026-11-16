package javalang

import (
	"encoding/json"
	"testing"
)

func TestBuildWorkspaceSendsAFullBuildAsAJSONString(t *testing.T) {
	h := newHarness(t, `{}`)
	h.fake.respond(func(method string, params json.RawMessage) any { return 1 })
	status, err := h.session.BuildWorkspace("com.example.Demo", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if status != BuildSucceeded {
		t.Errorf("status %d, want BuildSucceeded", status)
	}
	n := h.next(t)
	var p struct {
		Command   string   `json:"command"`
		Arguments []string `json:"arguments"`
	}
	if err := json.Unmarshal(n.params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Command != "vscode.java.buildWorkspace" || len(p.Arguments) != 1 {
		t.Fatalf("command %q arguments %v", p.Command, p.Arguments)
	}
	var opts struct {
		MainClass   string `json:"mainClass"`
		ProjectName string `json:"projectName"`
		IsFullBuild bool   `json:"isFullBuild"`
	}
	if err := json.Unmarshal([]byte(p.Arguments[0]), &opts); err != nil {
		t.Fatalf("the argument is not a JSON string of options: %v", err)
	}
	if opts.MainClass != "com.example.Demo" || opts.ProjectName != "demo" || !opts.IsFullBuild {
		t.Errorf("options %+v", opts)
	}
}

func TestBuildWorkspaceMapsJdtlsStatuses(t *testing.T) {
	// jdtls's BuildWorkspaceStatus: FAILED 0, SUCCEED 1, WITH_ERROR 2, CANCELLED 3.
	for code, want := range map[int]BuildStatus{
		0: BuildFailed, 1: BuildSucceeded, 2: BuildWithErrors, 3: BuildCancelled, 9: BuildFailed,
	} {
		h := newHarness(t, `{}`)
		h.fake.respond(func(method string, params json.RawMessage) any { return code })
		got, err := h.session.BuildWorkspace("A", "p")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("code %d: status %d, want %d", code, got, want)
		}
	}
}
