package dap

import "encoding/json"

// The protocol's types, as java-debug names them: a field it does not know
// is ignored, and one it needs and does not get fails the request with its
// reason.

// Source is a file of the debuggee.
type Source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// Thread is one thread of the debuggee.
type Thread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// StackFrame is one frame of a stopped thread. Line is 1-based.
type StackFrame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Source Source `json:"source"`
	Line   int    `json:"line"`
}

// Scope is one named group of a frame's variables, as Local and Static.
type Scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}

// Variable is a variable of a stopped frame, or one of its children.
// VariablesReference names the children to read, 0 for a leaf.
type Variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type"`
	VariablesReference int    `json:"variablesReference"`
	EvaluateName       string `json:"evaluateName"`
}

// Breakpoint is a breakpoint as the adapter has it: verified once its class
// is loaded, with the line it really is on.
type Breakpoint struct {
	ID       int    `json:"id"`
	Verified bool   `json:"verified"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
}

// LaunchArgs launches a Java program: java-debug's own fields, which name
// the class, where its paths are, and which java runs it.
type LaunchArgs struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Request     string            `json:"request"`
	MainClass   string            `json:"mainClass"`
	ProjectName string            `json:"projectName"`
	Cwd         string            `json:"cwd"`
	Args        string            `json:"args,omitempty"`
	VMArgs      string            `json:"vmArgs,omitempty"`
	ModulePaths []string          `json:"modulePaths,omitempty"`
	ClassPaths  []string          `json:"classPaths,omitempty"`
	JavaExec    string            `json:"javaExec,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	// Console keeps the program's output in the adapter's events, which
	// the debug console shows, rather than in a terminal it would ask the
	// IDE to open.
	Console string `json:"console,omitempty"`
	NoDebug bool   `json:"noDebug,omitempty"`
}

// InitializeArgs is the handshake: who the client is and how it counts.
type InitializeArgs struct {
	ClientID                     string `json:"clientID"`
	AdapterID                    string `json:"adapterID"`
	PathFormat                   string `json:"pathFormat"`
	LinesStartAt1                bool   `json:"linesStartAt1"`
	ColumnsStartAt1              bool   `json:"columnsStartAt1"`
	SupportsVariableType         bool   `json:"supportsVariableType"`
	SupportsRunInTerminalRequest bool   `json:"supportsRunInTerminalRequest"`
}

// Initialize shakes hands, and returns what the adapter can do.
func (c *Conn) Initialize() error {
	return c.Call("initialize", InitializeArgs{
		ClientID: "megafactory", AdapterID: "java", PathFormat: "path",
		LinesStartAt1: true, ColumnsStartAt1: true, SupportsVariableType: true,
	}, nil)
}

// Launch starts the debuggee, and its first stopped event follows.
func (c *Conn) Launch(args LaunchArgs) error { return c.Call("launch", args, nil) }

// ConfigurationDone tells the adapter that its breakpoints are all set, and
// it may start the debuggee.
func (c *Conn) ConfigurationDone() error { return c.Call("configurationDone", map[string]any{}, nil) }

// SetBreakpoints makes lines the file's breakpoints, replacing those it
// had: the protocol's set is the whole file's.
func (c *Conn) SetBreakpoints(path string, lines []int) ([]Breakpoint, error) {
	locs := make([]map[string]int, len(lines))
	for i, l := range lines {
		locs[i] = map[string]int{"line": l}
	}
	var out struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	err := c.Call("setBreakpoints", map[string]any{
		"source":      Source{Path: path},
		"lines":       lines,
		"breakpoints": locs,
	}, &out)
	return out.Breakpoints, err
}

// Threads lists the debuggee's threads.
func (c *Conn) Threads() ([]Thread, error) {
	var out struct {
		Threads []Thread `json:"threads"`
	}
	err := c.Call("threads", nil, &out)
	return out.Threads, err
}

// StackTrace lists a thread's frames, the innermost first.
func (c *Conn) StackTrace(threadID int) ([]StackFrame, error) {
	var out struct {
		StackFrames []StackFrame `json:"stackFrames"`
	}
	err := c.Call("stackTrace", map[string]any{"threadId": threadID, "startFrame": 0, "levels": 64}, &out)
	return out.StackFrames, err
}

// Scopes lists the scopes of one frame.
func (c *Conn) Scopes(frameID int) ([]Scope, error) {
	var out struct {
		Scopes []Scope `json:"scopes"`
	}
	err := c.Call("scopes", map[string]any{"frameId": frameID}, &out)
	return out.Scopes, err
}

// Variables lists the children of one reference: a scope's, or a variable's.
func (c *Conn) Variables(ref int) ([]Variable, error) {
	var out struct {
		Variables []Variable `json:"variables"`
	}
	err := c.Call("variables", map[string]any{"variablesReference": ref}, &out)
	return out.Variables, err
}

// Evaluate runs expression in the context of frameID, or of the top frame
// when it is 0. context is "watch" or "repl", which adapters treat alike.
func (c *Conn) Evaluate(expression string, frameID int, context string) (Variable, error) {
	var out struct {
		Result             string `json:"result"`
		Type               string `json:"type"`
		VariablesReference int    `json:"variablesReference"`
	}
	err := c.Call("evaluate", map[string]any{
		"expression": expression, "frameId": frameID, "context": context,
	}, &out)
	return Variable{Name: expression, Value: out.Result, Type: out.Type, VariablesReference: out.VariablesReference, EvaluateName: expression}, err
}

// Continue resumes the thread.
func (c *Conn) Continue(threadID int) error {
	return c.Call("continue", map[string]any{"threadId": threadID}, nil)
}

// Next steps over.
func (c *Conn) Next(threadID int) error {
	return c.Call("next", map[string]any{"threadId": threadID}, nil)
}

// StepIn steps into the next call.
func (c *Conn) StepIn(threadID int) error {
	return c.Call("stepIn", map[string]any{"threadId": threadID}, nil)
}

// StepOut runs to the end of the current call.
func (c *Conn) StepOut(threadID int) error {
	return c.Call("stepOut", map[string]any{"threadId": threadID}, nil)
}

// Pause interrupts a running thread.
func (c *Conn) Pause(threadID int) error {
	return c.Call("pause", map[string]any{"threadId": threadID}, nil)
}

// Disconnect ends the session, killing the debuggee when terminate is true.
func (c *Conn) Disconnect(terminate bool) error {
	return c.Call("disconnect", map[string]any{"terminateDebuggee": terminate}, nil)
}

// The events the app decodes.

// StoppedEvent says a thread stopped, and why.
type StoppedEvent struct {
	Reason      string `json:"reason"`
	ThreadID    int    `json:"threadId"`
	Description string `json:"description"`
	Text        string `json:"text"`
}

// OutputEvent is what the debuggee wrote, its category "stdout" or "stderr"
// and "console" for the adapter's own messages.
type OutputEvent struct {
	Category string `json:"category"`
	Output   string `json:"output"`
}

// Decode reads an event's body, which is empty for events that carry none.
func Decode(body json.RawMessage, v any) error {
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}
