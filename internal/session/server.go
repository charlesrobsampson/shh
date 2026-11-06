package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Server exposes a worker on a unix socket.
type Server struct {
	worker   *Worker
	listener *net.UnixListener
	path     string

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Listen claims the socket path for the worker. A leftover socket from a worker that died is replaced; a live worker or a file
// that is not a socket is an error.
func Listen(path string, w *Worker) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&fs.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket; refusing to remove it", path)
		}
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			conn.Close()
			return nil, fmt.Errorf("%w (socket %s)", ErrAlreadyRunning, path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return &Server{worker: w, listener: l, path: path, conns: map[net.Conn]struct{}{}}, nil
}

// Serve answers clients until ctx is cancelled, then closes every connection and removes the socket.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.listener.Close() // unlinks the socket
		s.mu.Lock()
		for conn := range s.conns {
			conn.Close()
		}
		s.mu.Unlock()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
				conn.Close()
			}()
			s.serveConn(ctx, conn)
		}()
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if encoder.Encode(Response{Code: "bad_request", Error: "malformed request: " + err.Error()}) != nil {
				return
			}
			continue
		}
		if req.Cmd == "subscribe" {
			s.stream(ctx, conn, encoder, scanner)
			return
		}
		if encoder.Encode(s.handle(req)) != nil {
			return
		}
	}
}

func (s *Server) handle(req Request) Response {
	w := s.worker
	var err error
	switch req.Cmd {
	case "status":
	case "baseline":
		err = withLevel(req, func(level int) error { return w.SetBaseline(level) })
	case "focus":
		err = withLevel(req, func(level int) error {
			_, err := w.Escalate(Escalation{Level: level, Duration: time.Duration(req.DurationSecs) * time.Second, Then: req.Then, Note: req.Note})
			return err
		})
	case "level":
		err = withLevel(req, func(level int) error { return w.SetLevel(level) })
	case "stop":
		if req.All {
			err = w.StopAll()
		} else {
			err = w.Stop()
		}
	case "pause":
		err = w.Pause()
	case "resume":
		err = w.Resume()
	case "adjust":
		err = w.Adjust(time.Duration(req.DeltaSecs) * time.Second)
	default:
		return Response{Code: "bad_request", Error: fmt.Sprintf("unknown command %q", req.Cmd)}
	}
	if err != nil {
		return Response{Code: codeFor(err), Error: err.Error()}
	}
	st, err := w.Status()
	if err != nil {
		return Response{Code: "internal", Error: err.Error()}
	}
	view := statusView(st, w.cfg.Clock.Now())
	return Response{OK: true, Status: &view}
}

func withLevel(req Request, do func(level int) error) error {
	if req.Level == nil {
		return fmt.Errorf("%w: %s needs a level", ErrInvalid, req.Cmd)
	}
	return do(*req.Level)
}

// stream acknowledges a subscription and then sends events until the client hangs up or the server stops.
func (s *Server) stream(ctx context.Context, conn net.Conn, encoder *json.Encoder, scanner *bufio.Scanner) {
	decisions, cancel := s.worker.Subscribe()
	defer cancel()
	if encoder.Encode(Response{OK: true}) != nil {
		return
	}
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for scanner.Scan() { // nothing more is expected; reading detects the hang-up
		}
	}()
	for {
		select {
		case d := <-decisions:
			n := d.Notification
			event := Event{Type: "decision", Alerted: d.Alerted, Notification: NotificationView{
				ID: n.ID, Source: n.Source, Sender: n.Sender, Title: n.Title, Body: n.Body, Time: n.Timestamp,
				Status: string(n.Status), Score: n.Score, Boost: n.Boost, Reason: n.Reason,
			}}
			if encoder.Encode(event) != nil {
				return
			}
		case <-gone:
			return
		case <-ctx.Done():
			return
		}
	}
}
