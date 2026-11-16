package javalang

import "encoding/json"

// BuildStatus is how a workspace build ended. The zero value is success,
// so that a server which says nothing of it fails nothing; the numbers jdtls
// sends are its BuildWorkspaceStatus ones, mapped in BuildWorkspace.
type BuildStatus int

const (
	BuildSucceeded BuildStatus = iota
	// BuildWithErrors compiled what it could and left compile errors, so
	// that classes of the files with errors are not the ones just written.
	BuildWithErrors
	BuildFailed
	BuildCancelled
)

// BuildWorkspace asks java-debug for a full build of the project, which
// returns once it has ended. A full build, not an incremental one: jdtls
// builds only the files it knows changed, and a file edited behind its back
// (another tool, a checkout) is one it does not know, so that an
// incremental build leaves its class as it was.
func (s *Session) BuildWorkspace(mainClass, projectName string) (BuildStatus, error) {
	conn, err := s.connection()
	if err != nil {
		return BuildFailed, err
	}
	// The command takes its options as one JSON string, not as an object.
	opts, err := json.Marshal(map[string]any{
		"mainClass":   mainClass,
		"projectName": projectName,
		"isFullBuild": true,
	})
	if err != nil {
		return BuildFailed, err
	}
	var code int
	err = conn.Call("workspace/executeCommand", map[string]any{
		"command":   "vscode.java.buildWorkspace",
		"arguments": []any{string(opts)},
	}, &code)
	if err != nil {
		return BuildFailed, err
	}
	switch code {
	case 1:
		return BuildSucceeded, nil
	case 2:
		return BuildWithErrors, nil
	case 3:
		return BuildCancelled, nil
	}
	return BuildFailed, nil
}
