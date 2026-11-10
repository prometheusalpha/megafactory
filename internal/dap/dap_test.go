package dap

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"
)

// pair is a connection under test and the adapter side it talks to.
func pair(t *testing.T) (*Conn, *bufio.Reader, *bufio.Writer) {
	t.Helper()
	client, adapter := net.Pipe()
	t.Cleanup(func() { client.Close(); adapter.Close() })
	c := NewConn(client, func(e Event) {})
	return c, bufio.NewReader(adapter), bufio.NewWriter(adapter)
}

// readMessage reads one raw message the connection sent.
func readMessage(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	body, err := readFrame(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return m
}

func write(t *testing.T, w *bufio.Writer, v any) {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// TestCallAnswers decodes a response's body, the threads of a stopped JVM.
func TestCallAnswers(t *testing.T) {
	c, r, w := pair(t)
	go func() {
		req := readMessage(t, r)
		if req["type"] != "request" || req["command"] != "threads" {
			t.Errorf("got %v %v", req["type"], req["command"])
		}
		write(t, w, map[string]any{
			"type": "response", "request_seq": req["seq"], "command": "threads", "success": true,
			"body": map[string]any{"threads": []map[string]any{{"id": 1, "name": "main"}}},
		})
	}()
	threads, err := c.Threads()
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Name != "main" || threads[0].ID != 1 {
		t.Fatalf("threads %+v", threads)
	}
}

// TestCallRefused carries the adapter's reason.
func TestCallRefused(t *testing.T) {
	c, r, w := pair(t)
	go func() {
		req := readMessage(t, r)
		write(t, w, map[string]any{
			"type": "response", "request_seq": req["seq"], "command": req["command"],
			"success": false, "message": "cwd is not a directory",
		})
	}()
	if _, err := c.StackTrace(1); err == nil || err.Error() != "cwd is not a directory" {
		t.Fatalf("err %v", err)
	}
}

// TestEventDecoded hands the event to the caller's callback.
func TestEventDecoded(t *testing.T) {
	events := make(chan StoppedEvent, 1)
	client, adapter := net.Pipe()
	t.Cleanup(func() { client.Close(); adapter.Close() })
	NewConn(client, func(e Event) {
		if e.Event != "stopped" {
			return
		}
		var s StoppedEvent
		if err := Decode(e.Body, &s); err != nil {
			t.Errorf("decode: %v", err)
		}
		events <- s
	})
	go func() {
		write(t, bufio.NewWriter(adapter), map[string]any{
			"type": "event", "event": "stopped",
			"body": map[string]any{"reason": "breakpoint", "threadId": 1},
		})
	}()
	select {
	case s := <-events:
		if s.Reason != "breakpoint" || s.ThreadID != 1 {
			t.Fatalf("stopped %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
}

// TestReverseRequestRefused answers one, so an adapter that sends a
// terminal request does not wait forever.
func TestReverseRequestRefused(t *testing.T) {
	_, r, w := pair(t)
	write(t, w, map[string]any{"type": "request", "seq": 7, "command": "runInTerminal"})
	resp := readMessage(t, r)
	if resp["type"] != "response" || resp["request_seq"] != float64(7) || resp["success"] != false {
		t.Fatalf("answer %v", resp)
	}
}

// TestClosedFailsCalls ends the calls still waiting, and refuses new ones.
func TestClosedFailsCalls(t *testing.T) {
	c, _, _ := pair(t)
	go c.Call("threads", nil, nil)
	c.Close()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection not done")
	}
	if err := c.Call("threads", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err %v", err)
	}
}

// TestSetBreakpointsSendsWholeFile passes the file's lines twice, as
// java-debug reads either shape.
func TestSetBreakpointsSendsWholeFile(t *testing.T) {
	c, r, w := pair(t)
	go func() {
		req := readMessage(t, r)
		args, _ := req["arguments"].(map[string]any)
		source, _ := args["source"].(map[string]any)
		if source["path"] != "/p/A.java" {
			t.Errorf("source %v", args["source"])
		}
		if lines, _ := args["lines"].([]any); len(lines) != 2 {
			t.Errorf("lines %v", args["lines"])
		}
		write(t, w, map[string]any{
			"type": "response", "request_seq": req["seq"], "command": req["command"], "success": true,
			"body": map[string]any{"breakpoints": []map[string]any{{"verified": true, "line": 12}}},
		})
	}()
	bps, err := c.SetBreakpoints("/p/A.java", []int{12, 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(bps) != 1 || !bps[0].Verified || bps[0].Line != 12 {
		t.Fatalf("breakpoints %+v", bps)
	}
}

// TestInitializeIdentifiesTheClient sends what java-debug expects of a
// 1-based, path-format client.
func TestInitializeIdentifiesTheClient(t *testing.T) {
	c, r, w := pair(t)
	go func() {
		req := readMessage(t, r)
		args, _ := req["arguments"].(map[string]any)
		if args["clientID"] != "megafactory" || args["adapterID"] != "java" || args["linesStartAt1"] != true {
			t.Errorf("initialize %v", args)
		}
		write(t, w, map[string]any{"type": "response", "request_seq": req["seq"], "command": "initialize", "success": true})
	}()
	if err := c.Initialize(); err != nil {
		t.Fatal(err)
	}
}
