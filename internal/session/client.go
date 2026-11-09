package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"syscall"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
)

const requestTimeout = 5 * time.Second

// Client talks to a worker over its socket. It dials for each request, so there is no connection to look after.
type Client struct {
	path string
}

func NewClient(socketPath string) *Client {
	return &Client{path: socketPath}
}

func (c *Client) Status() (StatusView, error) { return c.do(Request{Cmd: "status"}) }

func (c *Client) Baseline(level int) (StatusView, error) {
	return c.do(Request{Cmd: "baseline", Level: &level})
}

// Focus starts an escalation. duration 0 means until stopped; then may be nil.
func (c *Client) Focus(level int, duration time.Duration, then *int, note string) (StatusView, error) {
	return c.do(Request{Cmd: "focus", Level: &level, DurationSecs: int(duration / time.Second), Then: then, Note: note})
}

func (c *Client) SetLevel(level int) (StatusView, error) {
	return c.do(Request{Cmd: "level", Level: &level})
}

func (c *Client) Stop(all bool) (StatusView, error) { return c.do(Request{Cmd: "stop", All: all}) }

// ShowAlert has the worker show an alert, so it is laid out with the banners the worker already has on screen.
func (c *Client) ShowAlert(a alert.Alert) error {
	_, err := c.do(Request{Cmd: "alert", Alert: &AlertMsg{Kind: string(a.Kind), Source: a.Source, BundleID: a.BundleID, Title: a.Title, Body: a.Body}})
	return err
}

func (c *Client) Pause() (StatusView, error)  { return c.do(Request{Cmd: "pause"}) }
func (c *Client) Resume() (StatusView, error) { return c.do(Request{Cmd: "resume"}) }

func (c *Client) Adjust(delta time.Duration) (StatusView, error) {
	return c.do(Request{Cmd: "adjust", DeltaSecs: int(delta / time.Second)})
}

// Subscribe streams decisions until ctx is cancelled or the worker goes away; the channel is closed when it ends.
func (c *Client) Subscribe(ctx context.Context) (<-chan Event, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	conn.SetDeadline(time.Now().Add(requestTimeout))
	if err := json.NewEncoder(conn).Encode(Request{Cmd: "subscribe"}); err != nil {
		conn.Close()
		return nil, c.wrap(err)
	}
	if _, err := readResponse(scanner); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		defer conn.Close()
		go func() {
			<-ctx.Done()
			conn.Close()
		}()
		for scanner.Scan() {
			var e Event
			if json.Unmarshal(scanner.Bytes(), &e) != nil {
				continue
			}
			select {
			case events <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (c *Client) do(req Request) (StatusView, error) {
	conn, err := c.dial()
	if err != nil {
		return StatusView{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(requestTimeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return StatusView{}, c.wrap(err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	resp, err := readResponse(scanner)
	if err != nil {
		return StatusView{}, err
	}
	if resp.Status == nil {
		return StatusView{}, nil
	}
	return *resp.Status, nil
}

func readResponse(scanner *bufio.Scanner) (Response, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Response{}, err
		}
		return Response{}, errors.New("the worker closed the connection without answering")
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("unreadable answer from the worker: %w", err)
	}
	if !resp.OK {
		return Response{}, &RemoteError{Code: resp.Code, Message: resp.Error}
	}
	return resp, nil
}

func (c *Client) dial() (net.Conn, error) {
	conn, err := net.DialTimeout("unix", c.path, requestTimeout)
	if err != nil {
		return nil, c.wrap(err)
	}
	return conn, nil
}

// wrap turns "nobody is listening" into ErrNotRunning and leaves other errors alone.
func (c *Client) wrap(err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOTSOCK) {
		return fmt.Errorf("%w (no worker is listening on %s)", ErrNotRunning, c.path)
	}
	return err
}
