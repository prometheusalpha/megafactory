package javalang

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
