// Package smtptest runs a controllable SMTP server for adapter tests and the QA fixture.
package smtptest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// Mode selects how the server answers the end-of-data command.
type Mode string

const (
	// ModeAccept replies 250 and records the message.
	ModeAccept Mode = "accept"
	// ModeTransient replies 451 after receiving the message, without recording it.
	ModeTransient Mode = "transient"
	// ModePermanent replies 550 after receiving the message, without recording it.
	ModePermanent Mode = "permanent"
	// ModeHold records the message, then waits before replying until the mode
	// changes. Killing the client here simulates uncertain acceptance.
	ModeHold Mode = "hold"
)

// Message is one accepted email.
type Message struct {
	From string `json:"from"`
	To   string `json:"to"`
	Data string `json:"data"`
}

// State is the JSON view served to fixture controls.
type State struct {
	Mode     Mode      `json:"mode"`
	Held     int       `json:"held"`
	Messages []Message `json:"messages"`
}

// Server accepts plain-text SMTP without authentication on a loopback port.
type Server struct {
	listener net.Listener
	mu       sync.Mutex
	mode     Mode
	held     int
	released chan struct{}
	messages []Message
	holds    chan struct{}
	wg       sync.WaitGroup
}

// Start listens on an ephemeral loopback port and serves until Close.
func Start(ctx context.Context) (*Server, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{listener: listener, mode: ModeAccept, released: make(chan struct{}), holds: make(chan struct{}, 64)}
	s.wg.Go(s.serve)
	return s, nil
}

// Address is the host:port clients connect to.
func (s *Server) Address() string { return s.listener.Addr().String() }

// URL is the smtp:// form Memento's configuration accepts.
func (s *Server) URL() string { return "smtp://" + s.Address() }

// SetMode changes future replies and releases any held sessions, which then
// reply according to the new mode.
func (s *Server) SetMode(mode Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode != mode {
		close(s.released)
		s.released = make(chan struct{})
	}
	s.mode = mode
}

// Messages returns every accepted message in order.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

// Holds receives one value each time a session starts waiting in hold mode,
// so tests can act at that exact point instead of sleeping.
func (s *Server) Holds() <-chan struct{} { return s.holds }

// Held reports sessions waiting for a reply in hold mode.
func (s *Server) Held() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

// State snapshots mode, held sessions, and messages.
func (s *Server) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return State{Mode: s.mode, Held: s.held, Messages: append([]Message{}, s.messages...)}
}

// MarshalJSON serves State so fixture controls can encode the server directly.
func (s *Server) MarshalJSON() ([]byte, error) { return json.Marshal(s.State()) }

// Close stops listening and ends every session.
func (s *Server) Close() error {
	err := s.listener.Close()
	s.SetMode(ModeAccept)
	s.wg.Wait()
	return err
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Go(func() { s.session(conn) })
	}
}

func (s *Server) session(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	reader := bufio.NewReader(conn)
	reply := func(line string) bool {
		_, err := fmt.Fprint(conn, line+"\r\n")
		return err == nil
	}
	if !reply("220 smtptest ready") {
		return
	}
	var from, to string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(verb, "EHLO"):
			if !reply("250-smtptest\r\n250 8BITMIME") {
				return
			}
		case strings.HasPrefix(verb, "HELO"):
			if !reply("250 smtptest") {
				return
			}
		case strings.HasPrefix(verb, "MAIL FROM:"):
			from = address(line[len("MAIL FROM:"):])
			if !reply("250 OK") {
				return
			}
		case strings.HasPrefix(verb, "RCPT TO:"):
			to = address(line[len("RCPT TO:"):])
			if !reply("250 OK") {
				return
			}
		case verb == "DATA":
			if !reply("354 End data with <CR><LF>.<CR><LF>") {
				return
			}
			var data strings.Builder
			for {
				part, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if part == ".\r\n" || part == ".\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(part, "."))
			}
			if !s.finish(conn, Message{From: from, To: to, Data: data.String()}) {
				return
			}
			from, to = "", ""
		case verb == "RSET" || verb == "NOOP":
			if !reply("250 OK") {
				return
			}
		case verb == "QUIT":
			reply("221 Bye")
			return
		default:
			if !reply("502 Command not implemented") {
				return
			}
		}
	}
}

// finish records the message when the mode accepts it and sends the reply the
// current mode requires, waiting first while the mode is hold.
func (s *Server) finish(conn net.Conn, message Message) bool {
	s.mu.Lock()
	mode := s.mode
	if mode == ModeHold {
		s.messages = append(s.messages, message)
		s.held++
		select {
		case s.holds <- struct{}{}:
		default:
		}
	}
	for s.mode == ModeHold {
		released := s.released
		s.mu.Unlock()
		<-released
		s.mu.Lock()
	}
	if mode == ModeHold {
		s.held--
	}
	mode = s.mode
	if mode == ModeAccept && !slices.Contains(s.messages, message) {
		s.messages = append(s.messages, message)
	}
	s.mu.Unlock()
	var line string
	switch mode {
	case ModeTransient:
		line = "451 Requested action aborted: try again later"
	case ModePermanent:
		line = "550 Mailbox unavailable"
	default:
		line = "250 OK: queued"
	}
	_, err := fmt.Fprint(conn, line+"\r\n")
	return err == nil
}

// address extracts the path from "<user@host> PARAM=value".
func address(argument string) string {
	argument = strings.TrimSpace(argument)
	if start := strings.Index(argument, "<"); start >= 0 {
		if end := strings.Index(argument[start:], ">"); end > 0 {
			return argument[start+1 : start+end]
		}
	}
	return strings.Fields(argument + " ")[0]
}
