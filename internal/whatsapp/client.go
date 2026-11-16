// Package whatsapp runs the Baileys sidecar (sidecar/main.ts, under node) and talks to it in JSON
// lines: requests on its stdin, responses and events on its stdout.
package whatsapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
	"github.com/daffirds/bailey-cc-bridge/internal/proc"
)

// ErrClosed is returned by calls made after the sidecar exited.
var ErrClosed = errors.New("whatsapp sidecar is not running")

// AuthDir holds the Baileys credentials of the linked device.
func AuthDir() string { return filepath.Join(app.Home(), "auth") }

// Linked returns the phone number this Mac is linked to, read from the saved credentials.
func Linked() (number string, ok bool) {
	b, err := os.ReadFile(filepath.Join(AuthDir(), "creds.json"))
	if err != nil {
		return "", false
	}
	var creds struct {
		Me *struct {
			ID string `json:"id"`
		} `json:"me"`
	}
	if json.Unmarshal(b, &creds) != nil || creds.Me == nil || creds.Me.ID == "" {
		return "", false
	}
	number, _, _ = strings.Cut(creds.Me.ID, "@")
	number, _, _ = strings.Cut(number, ":")
	return number, true
}

type Options struct {
	Phone string // link with a pairing code for this number instead of a QR code (login only)
	Debug bool   // the sidecar logs at debug level
	Log   *slog.Logger
}

// Event is something the sidecar reports on its own.
type Event struct {
	Type    string   `json:"type"` // qr | pairing_code | ready | closed | message
	QR      string   `json:"qr,omitempty"`
	Code    string   `json:"code,omitempty"`
	Me      *Me      `json:"me,omitempty"`
	Reason  string   `json:"reason,omitempty"` // closed: logged_out | forbidden | …
	Message *Message `json:"message,omitempty"`
}

// Me is the linked account.
type Me struct {
	ID   string `json:"id"` // phone-number JID, e.g. 6281234567890@s.whatsapp.net
	LID  string `json:"lid,omitempty"`
	Name string `json:"name,omitempty"`
}

type Client struct {
	log   *slog.Logger
	group *proc.Group
	stdin io.WriteCloser

	writeMu sync.Mutex
	seq     atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan response
	closed  bool
	err     error

	events chan Event
	done   chan struct{}
}

type response struct {
	result json.RawMessage
	err    error
}

// Start installs the sidecar if needed and starts it. It connects to WhatsApp on its own; watch
// Events for "ready" (or "qr" when this Mac is not linked yet).
func Start(ctx context.Context, o Options) (*Client, error) {
	dir, err := install(ctx, o.Log)
	if err != nil {
		return nil, err
	}
	node, err := lookTool("node")
	if err != nil {
		return nil, errors.New("node not found: install Node.js 22.18 or newer (brew install node)")
	}
	if err := os.MkdirAll(AuthDir(), 0o700); err != nil {
		return nil, err
	}
	args := []string{"main.ts", "--auth", AuthDir()}
	if o.Phone != "" {
		args = append(args, "--phone", o.Phone)
	}
	if o.Debug {
		args = append(args, "--log-level", "debug")
	}
	cmd := exec.Command(node, args...)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{
		log:     o.Log,
		stdin:   stdin,
		pending: map[int64]chan response{},
		events:  make(chan Event, 1024),
		done:    make(chan struct{}),
	}
	if c.group, err = proc.Start(cmd); err != nil {
		return nil, fmt.Errorf("start whatsapp sidecar: %w", err)
	}
	var reads sync.WaitGroup
	reads.Add(2)
	go func() { defer reads.Done(); c.read(stdout) }()
	go func() { defer reads.Done(); c.logStderr(stderr) }()
	go func() {
		reads.Wait() // the pipes must be drained before Wait closes them
		err := cmd.Wait()
		c.mu.Lock()
		c.closed, c.err = true, err
		for id, ch := range c.pending {
			ch <- response{err: ErrClosed}
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.done)
	}()
	return c, nil
}

// Events delivers what the sidecar reports. It is never closed; watch Done as well.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when the sidecar exited; Err tells why.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close stops the sidecar. Closing its stdin lets it close the WhatsApp socket cleanly; it is
// killed if it takes longer than a few seconds.
func (c *Client) Close() error {
	c.writeMu.Lock()
	_ = c.stdin.Close()
	c.writeMu.Unlock()
	c.group.Stop(c.done, 5*time.Second)
	<-c.done
	return nil
}

// Call sends a request and waits for its response, decoding the result into out (if not nil).
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := c.seq.Add(1)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if params == nil {
		params = struct{}{}
	}
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.drop(id)
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && len(r.result) > 0 {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-ctx.Done():
		c.drop(id)
		return ctx.Err()
	}
}

func (c *Client) drop(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// read dispatches stdout lines: responses to their callers, everything else to Events.
func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			Type   string          `json:"type"`
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
			c.log.Debug("whatsapp sidecar stdout", "line", string(line))
			continue
		}
		if head.Type == "response" {
			c.mu.Lock()
			ch := c.pending[head.ID]
			delete(c.pending, head.ID)
			c.mu.Unlock()
			if ch != nil {
				if head.Error != "" {
					ch <- response{err: errors.New(head.Error)}
				} else {
					ch <- response{result: head.Result}
				}
			}
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			c.log.Warn("bad event from whatsapp sidecar", "err", err)
			continue
		}
		c.events <- ev
	}
}

// logStderr forwards the sidecar's logs: Baileys' pino JSON lines keep their level, anything
// else a library printed is debug noise.
func (c *Client) logStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		var p struct {
			Level int    `json:"level"`
			Msg   string `json:"msg"`
		}
		if json.Unmarshal([]byte(line), &p) != nil || p.Level == 0 {
			c.log.Debug("whatsapp sidecar", "out", line)
			continue
		}
		level := slog.LevelDebug
		switch {
		case p.Level >= 50:
			level = slog.LevelError
		case p.Level >= 40:
			level = slog.LevelWarn
		}
		c.log.Log(context.Background(), level, "whatsapp: "+p.Msg, "raw", line)
	}
}
