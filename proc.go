// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Adopted from: https://github.com/golang/go/blob/go1.24.3/src/cmd/go/internal/cache/prog.go

package cacheprogw

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/breezewish/go-cacheprogw/internal/quoted"
)

const (
	DEFAULT_TIMEOUT_HANDSHAKE = 5 * time.Second
	DEFAULT_TIMEOUT_RESPONSE  = 30 * time.Second
)

// Config is the configuration for talking to a cacheprog process.
type Config struct {
	HandshakeTimeout time.Duration
	ResponseTimeout  time.Duration
}

// DefaultConfig returns a default configuration.
func DefaultConfig() Config {
	return Config{
		HandshakeTimeout: DEFAULT_TIMEOUT_HANDSHAKE,
		ResponseTimeout:  DEFAULT_TIMEOUT_RESPONSE,
	}
}

// Proc wraps a process who implements Go's CacheProg protocol
// via JSON messages over stdin/stdout.
//
// See https://github.com/golang/go/issues/59719
type Proc struct {
	config Config

	cmd    *exec.Cmd
	stdout io.ReadCloser  // from the child process
	stdin  io.WriteCloser // to the child process
	bw     *bufio.Writer  // to stdin
	jenc   *json.Encoder  // to bw
	jdec   *json.Decoder  // from child proc stdout

	// can are the commands that the child process declared that it supports.
	// This is effectively the versioning mechanism.
	can map[Cmd]bool

	closing      atomic.Bool
	ctx          context.Context    // valid until Close via ctxClose
	ctxCancel    context.CancelFunc // called on Close
	readLoopDone chan struct{}      // closed when readLoop returns
	readLoopErr  error              // set if readLoop returns with an error, always assiged before readLoopDone

	mu       sync.Mutex // guards following fields
	nextID   int64
	inFlight map[int64]chan<- *Response

	// writeMu serializes writing to the child process.
	// It must never be held at the same time as mu.
	writeMu sync.Mutex
}

// Start starts the binary (with optional space-separated flags)
// and returns a handle that talks to it using the default configuration.
//
// It blocks a few seconds to wait for the child process to successfully start
// and advertise its capabilities.
func Start(progAndArgs string) (*Proc, error) {
	return StartWithConfig(progAndArgs, DefaultConfig())
}

// StartWithConfig starts the binary (with optional space-separated flags)
// and returns a handle that talks to it using the specified configuration.
//
// It blocks a few seconds to wait for the child process to successfully start
// and advertise its capabilities.
func StartWithConfig(progAndArgs string, config Config) (*Proc, error) {
	args, err := quoted.Split(progAndArgs)
	if err != nil {
		return nil, fmt.Errorf("invalid cacheprog args: %v", err)
	}
	var prog string
	if len(args) > 0 {
		prog = args[0]
		args = args[1:]
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, prog, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("error stdoutPipe to cacheprog: %v", err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("error stdinPipe to cacheprog: %v", err)
	}
	cmd.Stderr = os.Stderr // so we can see errors from the child process

	cmd.Cancel = func() error {
		in.Close()
		out.Close()
		return cmd.Process.Kill()
	}

	if err := cmd.Start(); err != nil {
		ctxCancel()
		return nil, fmt.Errorf("error starting cacheprog: %v", err)
	}

	pc := &Proc{
		config:    config,
		cmd:       cmd,
		stdout:    out,
		stdin:     in,
		bw:        bufio.NewWriter(in),
		ctx:       ctx,
		ctxCancel: ctxCancel,
	}
	pc.jenc = json.NewEncoder(pc.bw)
	pc.jdec = json.NewDecoder(pc.stdout)

	// Try to receive initial protocol message from the child process with a timeout.
	if err := pc.handleInitHandshake(); err != nil {
		ctxCancel()
		_ = cmd.Wait()
		return nil, fmt.Errorf("error reading initial handshake from cacheprog: %v", err)
	}

	pc.inFlight = make(map[int64]chan<- *Response)
	pc.readLoopDone = make(chan struct{})
	go pc.readLoop()

	return pc, nil
}

func (c *Proc) handleInitHandshake() error {
	res := new(Response)

	timeout := c.config.HandshakeTimeout
	if timeout <= 0 {
		timeout = DEFAULT_TIMEOUT_HANDSHAKE
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	decodeDone := make(chan error, 1)
	go func() {
		decodeDone <- c.jdec.Decode(res)
	}()

	select {
	case err := <-decodeDone:
		if err != nil {
			return err
		}
		can := map[Cmd]bool{}
		for _, cmd := range res.KnownCommands {
			can[cmd] = true
		}
		if len(can) == 0 {
			return fmt.Errorf("cacheprog declared no supported commands")
		}
		c.can = can
		return nil
	case <-timer.C:
		return fmt.Errorf("timed out")
	}
}

func (c *Proc) readLoop() {
	defer close(c.readLoopDone)

	for {
		res := new(Response)
		if err := c.jdec.Decode(res); err != nil {
			if c.closing.Load() {
				c.mu.Lock()
				for _, ch := range c.inFlight {
					close(ch)
				}
				c.inFlight = nil
				c.mu.Unlock()
				return // quit quietly without any errors
			}
			if err == io.EOF {
				// Note: Even when there are errors we will not early close the process.
				// Process is left running until Close() is called.
				c.readLoopErr = ErrCacheProgClosed
				return
			}
			c.readLoopErr = fmt.Errorf("error reading JSON from cacheprog: %v", err)
			return
		}

		c.mu.Lock()
		ch, ok := c.inFlight[res.ID]
		delete(c.inFlight, res.ID)
		c.mu.Unlock()
		if ok {
			ch <- res
		} else {
			c.readLoopErr = fmt.Errorf("cacheprog sent response for unknown request ID %v", res.ID)
			return
		}
	}
}

func (c *Proc) send(ctx context.Context, req *Request) (*Response, error) {
	resc := make(chan *Response, 1)
	if err := c.writeToChild(req, resc); err != nil {
		return nil, err
	}

	timeout := c.config.ResponseTimeout
	if timeout <= 0 {
		timeout = DEFAULT_TIMEOUT_RESPONSE
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-resc:
		if res == nil {
			return nil, ErrCacheProgClosed
		}
		if res.Err != "" {
			return nil, errors.New(res.Err)
		}
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for response from cacheprog")
	}
}

func (c *Proc) writeToChild(req *Request, resc chan<- *Response) (err error) {
	c.mu.Lock()
	if c.inFlight == nil {
		return ErrCacheProgClosed
	}
	c.nextID++
	req.ID = c.nextID
	c.inFlight[req.ID] = resc
	c.mu.Unlock()

	defer func() {
		if err != nil {
			c.mu.Lock()
			if c.inFlight != nil {
				delete(c.inFlight, req.ID)
			}
			c.mu.Unlock()
		}
	}()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.jenc.Encode(req); err != nil {
		return err
	}
	if err := c.bw.WriteByte('\n'); err != nil {
		return err
	}
	if req.Body != nil && req.BodySize > 0 {
		if err := c.bw.WriteByte('"'); err != nil {
			return err
		}
		e := base64.NewEncoder(base64.StdEncoding, c.bw)
		wrote, err := io.Copy(e, req.Body)
		if err != nil {
			return err
		}
		if err := e.Close(); err != nil {
			return nil
		}
		if wrote != req.BodySize {
			return fmt.Errorf("short write writing body to cacheprog for action %x, output %x: wrote %v; expected %v",
				req.ActionID, req.OutputID, wrote, req.BodySize)
		}
		if _, err := c.bw.WriteString("\"\n"); err != nil {
			return err
		}
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	return nil
}

// GetResult is the result of a successful Get call.
type GetResult struct {
	OutputID OutputID
	Size     int64
	Time     time.Time // Time is the time when the entry was put into the cache.
	DiskPath string
}

// Get retrieves the file from the cache.
// When cache is hit, the cacheprog is supposed to make file content available at the
// DiskPath (local).
func (c *Proc) Get(a ActionID) (GetResult, error) {
	if c.closing.Load() {
		return GetResult{}, ErrCacheProgClosed
	}
	if !c.can[CmdGet] {
		return GetResult{}, ErrUnsupportedCmd
	}

	res, err := c.send(c.ctx, &Request{
		Command:  CmdGet,
		ActionID: a[:],
	})
	if err != nil {
		return GetResult{}, err // TODO(bradfitz): or EntryNotFoundError? Audit callers.
	}
	if res.Miss {
		return GetResult{}, &EntryNotFoundError{}
	}
	e := GetResult{
		Size:     res.Size,
		DiskPath: res.DiskPath,
	}
	if res.Time != nil {
		e.Time = *res.Time
	} else {
		e.Time = time.Now()
	}
	if res.DiskPath == "" {
		return GetResult{}, &EntryNotFoundError{errors.New("cacheprog didn't populate DiskPath on get hit")}
	}
	if copy(e.OutputID[:], res.OutputID) != len(res.OutputID) {
		return GetResult{}, &EntryNotFoundError{errors.New("cacheprog response OutputID too short")}
	}
	return e, nil
}

// PutResult is the result of a successful Put call.
type PutResult struct {
	OutputID OutputID
	Size     int64
	DiskPath string
}

// Put stores the file in the cache and returns the OutputID and size.
// The cacheprog is supposed to also make file content available at the
// DiskPath (local).
func (c *Proc) Put(a ActionID, file io.ReadSeeker) (PutResult, error) {
	if c.closing.Load() {
		return PutResult{}, ErrCacheProgClosed
	}
	if !c.can[CmdPut] {
		return PutResult{}, ErrUnsupportedCmd
	}

	// Compute output ID.
	h := sha256.New()
	if _, err := file.Seek(0, 0); err != nil {
		return PutResult{}, err
	}
	size, err := io.Copy(h, file)
	if err != nil {
		return PutResult{}, err
	}
	var out OutputID
	h.Sum(out[:0])

	if _, err := file.Seek(0, 0); err != nil {
		return PutResult{}, err
	}

	res, err := c.send(c.ctx, &Request{
		Command:  CmdPut,
		ActionID: a[:],
		OutputID: out[:],
		Body:     file,
		BodySize: size,
	})
	if err != nil {
		return PutResult{}, err
	}
	if res.DiskPath == "" {
		return PutResult{}, errors.New("cacheprog didn't populate DiskPath on put")
	}
	return PutResult{OutputID: out, Size: size, DiskPath: res.DiskPath}, nil
}

// Close closes the process and waits for it to exit.
func (c *Proc) Close() error {
	if c.closing.Load() {
		return nil // already closed
	}

	c.closing.Store(true)

	// First write a "close" message to the child so it can exit nicely
	// and clean up if it wants. Only after that exchange do we cancel
	// the context that kills the process.
	if c.can[CmdClose] {
		_, _ = c.send(c.ctx, &Request{Command: CmdClose})
	}
	// Cancel the context, which will close the helper's stdin.
	c.ctxCancel()
	// Wait until the helper closes its stdout.
	<-c.readLoopDone

	_ = c.cmd.Wait()

	return c.readLoopErr
}
