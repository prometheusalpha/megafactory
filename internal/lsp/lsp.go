// Package lsp is a client of language servers: JSON-RPC 2.0 over a
// stream, framed by Content-Length headers. It carries messages and
// knows none of the protocol's types beyond what a caller gives it.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Error is a response's error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message) }

// ErrClosed is a call on a connection that has ended, or that ends before
// the answer comes.
var ErrClosed = errors.New("lsp: connection closed")

type message struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *Error           `json:"error,omitempty"`
}

// Handler answers what the server sends: a notification, which wants no
// answer, or a request, whose result or error goes back. Handlers run in
// order on the reading goroutine, so a slow one holds up the rest.
type Handler interface {
	Notify(method string, params json.RawMessage)
	Request(method string, params json.RawMessage) (any, error)
}

// Conn is one connection to a server. Its methods are safe to call from
// several goroutines.
type Conn struct {
	w  io.Writer
	wm sync.Mutex

	mu      sync.Mutex
	next    int64
	pending map[int64]chan message
	closed  bool

	done chan struct{}
}

// NewConn reads the server's messages from r and writes to w until r
// ends. h may be nil, which refuses requests and drops notifications.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	c := &Conn{w: w, pending: map[int64]chan message{}, done: make(chan struct{})}
	go c.read(bufio.NewReader(r), h)
	return c
}

// Done is closed when the server's output ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Call sends a request and waits for its result, decoded into result,
// which may be nil to drop it.
func (c *Conn) Call(method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.next++
	id := c.next
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	m, ok := <-ch
	if !ok {
		return ErrClosed
	}
	if m.Error != nil {
		return m.Error
	}
	if result != nil && len(m.Result) > 0 {
		return json.Unmarshal(m.Result, result)
	}
	return nil
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Conn) send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wm.Lock()
	defer c.wm.Unlock()
	_, err = fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func (c *Conn) read(r *bufio.Reader, h Handler) {
	defer func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.done)
	}()
	for {
		body, err := readFrame(r)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch {
		case m.Method == "" && m.ID != nil:
			var id int64
			if json.Unmarshal(*m.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.ID != nil:
			c.answer(h, m)
		case h != nil:
			h.Notify(m.Method, m.Params)
		}
	}
}

func (c *Conn) answer(h Handler, m message) {
	resp := map[string]any{"jsonrpc": "2.0", "id": m.ID}
	var (
		result any
		err    error
	)
	if h == nil {
		err = &Error{Code: -32601, Message: "method not found: " + m.Method}
	} else {
		result, err = h.Request(m.Method, m.Params)
	}
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: -32603, Message: err.Error()}
		}
		resp["error"] = e
	} else {
		// A result is always there, though null.
		resp["result"] = result
	}
	c.send(resp)
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
			n, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
		}
	}
	if n < 0 {
		return nil, errors.New("lsp: frame without Content-Length")
	}
	body := make([]byte, n)
	_, err := io.ReadFull(r, body)
	return body, err
}
