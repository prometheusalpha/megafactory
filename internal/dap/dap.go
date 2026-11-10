// Package dap is a client of debug adapters: the Debug Adapter Protocol's
// Content-Length framed JSON over a socket, the requests java-debug answers,
// and the events it sends. It knows nothing of the app's panels: the caller
// decodes the events it acts on.
package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// callTimeout bounds one request, so a dead adapter fails the call rather
// than holding the app that asked.
const callTimeout = 20 * time.Second

// ErrClosed is a call on a connection that has ended, or that ends before
// the answer comes.
var ErrClosed = errors.New("dap: connection closed")

// Event is what an adapter said happened, its body undecoded so the caller
// decodes only the events it acts on.
type Event struct {
	Event string
	Body  json.RawMessage
}

// frame is one message of the protocol. What a message is depends on Type,
// so the fields of the others stay empty.
type frame struct {
	Type       string          `json:"type"`
	Seq        int             `json:"seq"`
	RequestSeq int             `json:"request_seq"`
	Command    string          `json:"command"`
	Event      string          `json:"event"`
	Success    bool            `json:"success"`
	Message    string          `json:"message"`
	Body       json.RawMessage `json:"body"`
}

// Conn is one connection to a debug adapter. Reading runs in its own
// goroutine and calls onEvent there, so a caller hands those to its UI
// thread; Call may be used from any goroutine.
type Conn struct {
	r       *bufio.Reader
	c       io.WriteCloser
	onEvent func(Event)

	wm sync.Mutex

	mu      sync.Mutex
	seq     int
	pending map[int]chan frame
	closed  bool
	done    chan struct{}
}

// Dial connects to an adapter listening at addr, as java-debug's session
// does on a loopback port. ctx bounds the dial only; Close ends the
// connection.
func Dial(ctx context.Context, addr string, onEvent func(Event)) (*Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return NewConn(c, onEvent), nil
}

// NewConn reads the adapter's messages from c until it ends, and answers
// them through onEvent, which may be nil.
func NewConn(c io.ReadWriteCloser, onEvent func(Event)) *Conn {
	conn := &Conn{r: bufio.NewReader(c), c: c, onEvent: onEvent, pending: map[int]chan frame{}, done: make(chan struct{})}
	go conn.read()
	return conn
}

// Done is closed when the adapter's output ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Close ends the connection, failing the calls still waiting.
func (c *Conn) Close() error { return c.c.Close() }

// Call sends a request and waits for its answer, decoded into result, which
// may be nil to drop it. An adapter that refuses the request fails the call
// with the reason it gave.
func (c *Conn) Call(command string, args, result any) error {
	seq, ch, err := c.ask(command)
	if err != nil {
		return err
	}
	msg := map[string]any{"seq": seq, "type": "request", "command": command}
	if args != nil {
		msg["arguments"] = args
	}
	if err := c.send(msg); err != nil {
		c.forget(seq)
		return err
	}
	select {
	case f, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if !f.Success {
			if f.Message == "" {
				f.Message = command + " failed"
			}
			return errors.New(f.Message)
		}
		if result != nil && len(f.Body) > 0 {
			return json.Unmarshal(f.Body, result)
		}
		return nil
	case <-time.After(callTimeout):
		c.forget(seq)
		return fmt.Errorf("%s timed out", command)
	}
}

// ask takes the next sequence number and the channel its answer comes on.
func (c *Conn) ask(command string) (int, chan frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, nil, ErrClosed
	}
	c.seq++
	ch := make(chan frame, 1)
	c.pending[c.seq] = ch
	return c.seq, ch, nil
}

func (c *Conn) forget(seq int) {
	c.mu.Lock()
	delete(c.pending, seq)
	c.mu.Unlock()
}

// seqNext numbers a message the connection sends on its own, as the refusal
// of a reverse request.
func (c *Conn) seqNext() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}

func (c *Conn) send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wm.Lock()
	defer c.wm.Unlock()
	_, err = fmt.Fprintf(c.c, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func (c *Conn) read() {
	defer func() {
		c.mu.Lock()
		c.closed = true
		for seq, ch := range c.pending {
			close(ch)
			delete(c.pending, seq)
		}
		c.mu.Unlock()
		c.c.Close()
		close(c.done)
	}()
	for {
		body, err := readFrame(c.r)
		if err != nil {
			return
		}
		var f frame
		if json.Unmarshal(body, &f) != nil {
			continue
		}
		switch f.Type {
		case "response":
			c.mu.Lock()
			ch := c.pending[f.RequestSeq]
			delete(c.pending, f.RequestSeq)
			c.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "event":
			if c.onEvent != nil {
				c.onEvent(Event{Event: f.Event, Body: f.Body})
			}
		case "request":
			// A launch asks for no reverse request; answering keeps an
			// adapter that sends one from waiting forever.
			c.send(map[string]any{
				"seq": c.seqNext(), "type": "response", "request_seq": f.Seq,
				"success": false, "command": f.Command, "message": "unsupported request: " + f.Command,
			})
		}
	}
}

// readFrame reads one message: its headers, then the body they count.
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
		return nil, errors.New("dap: message without a length")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
