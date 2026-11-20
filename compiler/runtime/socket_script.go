package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ScriptedSocket is a SocketBackend that plays scripted peers instead of the
// network (ADR 0060 §6). Each dial takes the next peer in Peers, in order,
// and that peer's Steps say what the program must write and what its reads
// receive. A program that departs from the script gets a fault naming the
// difference, so a protocol library's test fails on the first wrong byte.
type ScriptedSocket struct {
	Peers []*SocketPeer

	mu   sync.Mutex
	next int
}

// SocketPeer is one scripted connection.
type SocketPeer struct {
	Host string
	Port int
	// DialError, when set, is what connecting to this peer fails with.
	DialError *SocketFailure
	Steps     []SocketStep

	mu     sync.Mutex
	pos    int
	offset int // bytes of Steps[pos] already written or read
	closed bool
	stall  chan struct{}
}

// SocketStep is one step of a peer's script. Exactly one field is set.
type SocketStep struct {
	// Expect is bytes the program must write next, in any number of writes.
	Expect []byte
	// Send is bytes the program's next reads receive.
	Send []byte
	// StartTLS means the program must call startTls next. A non-empty
	// TLSError makes the handshake fail with that reason.
	StartTLS bool
	TLSError string
	// Hangup means the peer closes: every later read is Err(Closed).
	Hangup bool
	// Stall means the next read never completes on its own, so only the
	// call's deadline ends it.
	Stall bool
}

// Closed reports whether the program, or its owning call, closed this
// connection.
func (p *SocketPeer) Closed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// Done reports every peer not dialed and every step not reached.
func (s *ScriptedSocket) Done() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var problems []string
	for i, p := range s.Peers {
		if i >= s.next {
			problems = append(problems, fmt.Sprintf("peer %s:%d was never dialed", p.Host, p.Port))
			continue
		}
		p.mu.Lock()
		if p.pos < len(p.Steps) && !(p.Steps[p.pos].Hangup && p.pos == len(p.Steps)-1) {
			problems = append(problems, fmt.Sprintf("peer %s:%d stopped at step %d of %d (%s)", p.Host, p.Port, p.pos+1, len(p.Steps), p.Steps[p.pos].describe()))
		}
		p.mu.Unlock()
	}
	if len(problems) > 0 {
		return fmt.Errorf("socket script not finished: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (s *ScriptedSocket) Dial(_ context.Context, host string, port int) (Transport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.Peers) {
		return nil, fmt.Errorf("socket script has no peer left for %s:%d", host, port)
	}
	p := s.Peers[s.next]
	s.next++
	if p.Host != host || p.Port != port {
		return nil, fmt.Errorf("socket script expected a connection to %s:%d, got %s:%d", p.Host, p.Port, host, port)
	}
	if p.DialError != nil {
		return nil, p.DialError
	}
	p.stall = make(chan struct{})
	return &scriptedConn{peer: p}, nil
}

func (s *ScriptedSocket) StartTLS(_ context.Context, t Transport, _ string) (Transport, error) {
	c, ok := t.(*scriptedConn)
	if !ok {
		return nil, fmt.Errorf("startTls: transport %T is not scripted", t)
	}
	p := c.peer
	p.mu.Lock()
	defer p.mu.Unlock()
	step, err := p.current("startTls")
	if err != nil {
		return nil, err
	}
	if !step.StartTLS {
		return nil, fmt.Errorf("socket script expected %s, got startTls", step.describe())
	}
	p.advance()
	if step.TLSError != "" {
		return nil, &SocketFailure{Variant: "Tls", Reason: step.TLSError}
	}
	return c, nil
}

func (st SocketStep) describe() string {
	switch {
	case st.Expect != nil:
		return fmt.Sprintf("a write of %q", st.Expect)
	case st.Send != nil:
		return fmt.Sprintf("a read of %q", st.Send)
	case st.StartTLS:
		return "startTls"
	case st.Hangup:
		return "the peer hanging up"
	case st.Stall:
		return "a read that stalls"
	}
	return "an empty step"
}

func (p *SocketPeer) current(op string) (SocketStep, error) {
	if p.closed {
		return SocketStep{}, io.ErrClosedPipe
	}
	if p.pos >= len(p.Steps) {
		return SocketStep{}, fmt.Errorf("socket script for %s:%d has ended, but the program called %s", p.Host, p.Port, op)
	}
	return p.Steps[p.pos], nil
}

func (p *SocketPeer) advance() {
	p.pos++
	p.offset = 0
}

type scriptedConn struct{ peer *SocketPeer }

func (c *scriptedConn) Read(buf []byte) (int, error) {
	p := c.peer
	p.mu.Lock()
	step, err := p.current("read")
	if err != nil {
		p.mu.Unlock()
		return 0, err
	}
	switch {
	case step.Hangup:
		p.mu.Unlock()
		return 0, io.EOF
	case step.Stall:
		stall := p.stall
		p.mu.Unlock()
		<-stall
		return 0, errDeadline
	case step.Send != nil:
		n := copy(buf, step.Send[p.offset:])
		p.offset += n
		if p.offset == len(step.Send) {
			p.advance()
		}
		p.mu.Unlock()
		return n, nil
	}
	p.mu.Unlock()
	return 0, fmt.Errorf("socket script expected %s, got a read", step.describe())
}

func (c *scriptedConn) Write(data []byte) (int, error) {
	p := c.peer
	p.mu.Lock()
	defer p.mu.Unlock()
	written := 0
	for written < len(data) {
		step, err := p.current("write")
		if err != nil {
			return written, err
		}
		if step.Expect == nil {
			return written, fmt.Errorf("socket script expected %s, got a write of %q", step.describe(), data[written:])
		}
		want := step.Expect[p.offset:]
		got := data[written:]
		n := min(len(want), len(got))
		if !bytes.Equal(want[:n], got[:n]) {
			return written, fmt.Errorf("socket script expected a write of %q, got %q", want, got)
		}
		written += n
		p.offset += n
		if p.offset == len(step.Expect) {
			p.advance()
		}
	}
	return written, nil
}

func (c *scriptedConn) Close() error {
	p := c.peer
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.releaseStall()
	return nil
}

// staleWhileIdle is the peer having closed the connection or having bytes
// waiting for the program, checked when Socket.reuse takes it (ADR 0109
// §4). A script that has ended is idle, not stale.
func (c *scriptedConn) staleWhileIdle() bool {
	p := c.peer
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return true
	}
	if p.pos >= len(p.Steps) {
		return false
	}
	st := p.Steps[p.pos]
	return st.Hangup || st.Send != nil
}

// SetDeadline releases a stalled read; the provider calls it when the call's
// context is done.
func (c *scriptedConn) SetDeadline(time.Time) error {
	p := c.peer
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseStall()
	return nil
}

func (p *SocketPeer) releaseStall() {
	select {
	case <-p.stall:
	default:
		close(p.stall)
	}
}

type deadlineError struct{}

func (deadlineError) Error() string { return "i/o timeout" }

var errDeadline error = deadlineError{}
