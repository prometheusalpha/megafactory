package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

// pair connects a Conn to a fake server, whose side is the returned
// reader and writer.
func pair(t *testing.T, h Handler) (*Conn, *bufio.Reader, io.Writer) {
	t.Helper()
	sr, cw := io.Pipe() // client writes, server reads
	cr, sw := io.Pipe() // server writes, client reads
	t.Cleanup(func() { sw.Close(); cw.Close() })
	return NewConn(cr, cw, h), bufio.NewReader(sr), sw
}

func serverRead(t *testing.T, r *bufio.Reader) message {
	t.Helper()
	body, err := readFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func serverSend(t *testing.T, w io.Writer, v any) {
	t.Helper()
	body, _ := json.Marshal(v)
	if _, err := io.WriteString(w, "Content-Length: "+itoa(len(body))+"\r\n\r\n"+string(body)); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestCallMatchesResponseByID(t *testing.T) {
	c, r, w := pair(t, nil)
	errs := make(chan error, 2)
	var a, b string
	go func() { errs <- c.Call("a", nil, &a) }()
	ma := serverRead(t, r)
	go func() { errs <- c.Call("b", nil, &b) }()
	mb := serverRead(t, r)
	// Answered out of order.
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(*mb.ID), "result": "B"})
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(*ma.ID), "result": "A"})
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if a != "A" || b != "B" {
		t.Errorf("a=%q b=%q", a, b)
	}
}

func TestCallError(t *testing.T) {
	c, r, w := pair(t, nil)
	errc := make(chan error, 1)
	go func() { errc <- c.Call("x", nil, nil) }()
	m := serverRead(t, r)
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(*m.ID), "error": Error{Code: -32000, Message: "boom"}})
	var e *Error
	if err := <-errc; !errors.As(err, &e) || e.Code != -32000 || e.Message != "boom" {
		t.Errorf("err %v", err)
	}
}

func TestCallFailsWhenServerEnds(t *testing.T) {
	c, r, w := pair(t, nil)
	errc := make(chan error, 1)
	go func() { errc <- c.Call("x", nil, nil) }()
	serverRead(t, r)
	w.(io.Closer).Close()
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Errorf("pending call: %v", err)
	}
	<-c.Done()
	if err := c.Call("y", nil, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("call after end: %v", err)
	}
}

type recorder struct{ notes chan string }

func (h recorder) Notify(method string, params json.RawMessage) { h.notes <- method + string(params) }
func (h recorder) Request(method string, params json.RawMessage) (any, error) {
	if method == "fail" {
		return nil, errors.New("no")
	}
	return map[string]string{"got": method}, nil
}

func TestServerNotificationsAndRequests(t *testing.T) {
	h := recorder{make(chan string, 1)}
	_, r, w := pair(t, h)
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "method": "window/logMessage", "params": map[string]int{"type": 3}})
	select {
	case got := <-h.notes:
		if got != `window/logMessage{"type":3}` {
			t.Errorf("notification %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification")
	}
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "client/registerCapability", "params": map[string]any{}})
	m := serverRead(t, r)
	if string(*m.ID) != "7" || string(m.Result) != `{"got":"client/registerCapability"}` {
		t.Errorf("answer %s %s", *m.ID, m.Result)
	}
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "fail"})
	if m := serverRead(t, r); m.Error == nil || m.Error.Code != -32603 || m.Error.Message != "no" {
		t.Errorf("failed answer %+v", m)
	}
}

func TestUnhandledRequestIsRefused(t *testing.T) {
	_, r, w := pair(t, nil)
	serverSend(t, w, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "workspace/configuration"})
	if m := serverRead(t, r); m.Error == nil || m.Error.Code != -32601 {
		t.Errorf("answer %+v", m)
	}
}
