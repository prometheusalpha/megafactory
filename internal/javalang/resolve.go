package javalang

import "fmt"

// MainClass is one entry of java-debug's resolveMainClass result.
type MainClass struct {
	MainClass   string `json:"mainClass"`
	ProjectName string `json:"projectName"`
	FilePath    string `json:"filePath"`
}

// ResolveMainClasses asks java-debug for the project's runnable classes.
func (s *Session) ResolveMainClasses() ([]MainClass, error) {
	conn, err := s.connection()
	if err != nil {
		return nil, err
	}
	var out []MainClass
	err = conn.Call("workspace/executeCommand", map[string]any{
		"command":   "vscode.java.resolveMainClass",
		"arguments": []any{},
	}, &out)
	return out, err
}

// ResolveClasspath returns the module paths and class paths of one main
// class. The command's result is [modulePaths, classPaths]; a short or null
// result leaves the missing side empty rather than failing.
func (s *Session) ResolveClasspath(mainClass, projectName string) (modulePaths, classPaths []string, err error) {
	conn, err := s.connection()
	if err != nil {
		return nil, nil, err
	}
	var paths [][]string
	err = conn.Call("workspace/executeCommand", map[string]any{
		"command":   "vscode.java.resolveClasspath",
		"arguments": []any{mainClass, projectName},
	}, &paths)
	if err != nil {
		return nil, nil, err
	}
	if len(paths) > 0 {
		modulePaths = paths[0]
	}
	if len(paths) > 1 {
		classPaths = paths[1]
	}
	return modulePaths, classPaths, nil
}

// StartDebugSession asks java-debug for the port its adapter listens on, to
// which the debug client connects with the Debug Adapter Protocol. A result
// that is not a port fails rather than connecting nowhere.
func (s *Session) StartDebugSession() (int, error) {
	conn, err := s.connection()
	if err != nil {
		return 0, err
	}
	var port int
	if err := conn.Call("workspace/executeCommand", map[string]any{
		"command":   "vscode.java.startDebugSession",
		"arguments": []any{},
	}, &port); err != nil {
		return 0, err
	}
	if port <= 0 {
		return 0, fmt.Errorf("java-debug started no session: port %d", port)
	}
	return port, nil
}
