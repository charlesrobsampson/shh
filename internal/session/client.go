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
	return c.FocusAlert(level, duration, then, note, "")
}

// FocusAlert starts a session that announces its end: alert is none, sound or banner ("" means none).
func (c *Client) FocusAlert(level int, duration time.Duration, then *int, note, alert string) (StatusView, error) {
	return c.do(Request{Cmd: "focus", Level: &level, DurationSecs: int(duration / time.Second), Then: then, Note: note, SessionAlert: alert})
}

// FocusQueued keeps a session to start when the running routine ends.
func (c *Client) FocusQueued(level int, duration time.Duration, then *int, note, alert string) (StatusView, error) {
	return c.do(Request{Cmd: "focus", Level: &level, DurationSecs: int(duration / time.Second), Then: then, Note: note, SessionAlert: alert, QueueAfterRoutine: true})
}

// FocusAfterRoutine starts a session on top of a running routine, which waits for it to end and then carries on.
func (c *Client) FocusAfterRoutine(level int, duration time.Duration, then *int, note, alert string) (StatusView, error) {
	return c.do(Request{Cmd: "focus", Level: &level, DurationSecs: int(duration / time.Second), Then: then, Note: note, SessionAlert: alert, ContinueRoutine: true})
}

func (c *Client) SetLevel(level int) (StatusView, error) {
	return c.do(Request{Cmd: "level", Level: &level})
}

func (c *Client) Stop(all bool) (StatusView, error) { return c.do(Request{Cmd: "stop", All: all}) }

// ShowAlert has the worker show an alert, so it is laid out with the banners the worker already has on screen.
func (c *Client) ShowAlert(a alert.Alert) error {
	_, err := c.do(Request{Cmd: "alert", Alert: &AlertMsg{Kind: string(a.Kind), Source: a.Source, BundleID: a.BundleID, Title: a.Title, Body: a.Body, NotificationID: a.NotificationID}})
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
	resp, err := c.call(req)
	if err != nil || resp.Status == nil {
		return StatusView{}, err
	}
	return *resp.Status, nil
}

// call sends one request and returns the worker's whole answer.
func (c *Client) call(req Request) (Response, error) {
	conn, err := c.dial()
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(requestTimeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, c.wrap(err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return readResponse(scanner)
}

// RoutineRun starts a saved routine.
func (c *Client) RoutineRun(name string) (RoutineInfo, error) {
	return c.routine(Request{Cmd: "routine_run", Name: name})
}

// RoutineNext starts the step a waiting routine is waiting on, or skips to the next step of a running one.
func (c *Client) RoutineNext() (RoutineInfo, error) { return c.routine(Request{Cmd: "routine_next"}) }

// RoutineBack restarts the step before the one in focus.
func (c *Client) RoutineBack() (RoutineInfo, error) { return c.routine(Request{Cmd: "routine_back"}) }

// RoutineUnqueue drops the session that was to start when the routine ends.
func (c *Client) RoutineUnqueue() (RoutineInfo, error) {
	return c.routine(Request{Cmd: "routine_unqueue"})
}

// WorkerInfo asks the worker about itself.
func (c *Client) WorkerInfo() (WorkerInfo, error) {
	resp, err := c.call(Request{Cmd: "worker_info"})
	if err != nil || resp.Worker == nil {
		return WorkerInfo{}, err
	}
	return *resp.Worker, nil
}

// WorkerQuit asks the worker to shut down. It returns once the worker has heard; it may take a moment to go.
func (c *Client) WorkerQuit() error {
	_, err := c.call(Request{Cmd: "worker_quit"})
	return err
}

// WaitGone waits until the worker with this process id has gone, up to wait (five seconds when zero). Not answering on the socket is
// not enough: the worker is still letting go of its lock and its files for a moment after that, and a new one started then would fail.
func (c *Client) WaitGone(pid int, wait time.Duration) error {
	if wait == 0 {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := c.Status(); errors.Is(err, ErrNotRunning) && !processAlive(pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the worker did not stop within %v", wait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// processAlive says whether a process with that id exists.
func processAlive(pid int) bool {
	if pid <= 0 || pid == os.Getpid() { // a worker running inside this process (in tests) has no process of its own to wait for
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// RoutineSkip leaves out a routine's next scheduled start; RoutineUnskip takes that back.
func (c *Client) RoutineSkip(name string) (RoutineInfo, error) {
	return c.routine(Request{Cmd: "routine_skip", Name: name})
}
func (c *Client) RoutineUnskip(name string) (RoutineInfo, error) {
	return c.routine(Request{Cmd: "routine_unskip", Name: name})
}

// RoutineStop ends the routine run.
func (c *Client) RoutineStop() (RoutineInfo, error) { return c.routine(Request{Cmd: "routine_stop"}) }

// RoutineStatus is the routine run in progress and the last one that ended.
func (c *Client) RoutineStatus() (RoutineInfo, error) {
	return c.routine(Request{Cmd: "routine_status"})
}

func (c *Client) routine(req Request) (RoutineInfo, error) {
	resp, err := c.call(req)
	if err != nil || resp.Routine == nil {
		return RoutineInfo{}, err
	}
	return *resp.Routine, nil
}

// Train labels a stored notification, teaching the worker's model, and says how its score moved.
func (c *Client) Train(req TrainRequest) (TrainView, error) {
	resp, err := c.call(Request{Cmd: "train", NotificationID: req.NotificationID, Label: req.Label, Feedback: req.Feedback})
	if err != nil || resp.Train == nil {
		return TrainView{}, err
	}
	return *resp.Train, nil
}

// UndoTraining reverses the worker's most recent training exactly and says which notification it was about.
func (c *Client) UndoTraining() (TrainView, error) {
	resp, err := c.call(Request{Cmd: "undo"})
	if err != nil || resp.Train == nil {
		return TrainView{}, err
	}
	return *resp.Train, nil
}

// ResetModel sends the worker's model back to the seeded one; RollbackModel goes back to the last checkpoint and says how many
// notifications were marked untrained again.
func (c *Client) ResetModel() error {
	_, err := c.call(Request{Cmd: "model_reset"})
	return err
}

// ImportHistory has the worker store what macOS holds from the last while as unlabeled history, and says how many were new.
func (c *Client) ImportHistory(since time.Duration) (int, error) {
	resp, err := c.call(Request{Cmd: "import_history", DurationSecs: int(since / time.Second)})
	if err != nil || resp.Train == nil {
		return 0, err
	}
	return resp.Train.Count, nil
}

func (c *Client) RollbackModel() (int, error) {
	resp, err := c.call(Request{Cmd: "model_rollback"})
	if err != nil || resp.Train == nil {
		return 0, err
	}
	return resp.Train.Count, nil
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

// Ensure makes sure a worker is answering on the client's socket, starting one with spawn if none is, and returns its status. With no
// spawn function it reports that a worker cannot be started from here. wait is how long to give a started worker to answer; five
// seconds when zero.
func Ensure(c *Client, spawn func() error, wait time.Duration) (StatusView, error) {
	view, err := c.Status()
	if err == nil || !errors.Is(err, ErrNotRunning) {
		return view, err
	}
	if spawn == nil {
		return StatusView{}, fmt.Errorf("%w, and a worker cannot be started from here", err)
	}
	if err := spawn(); err != nil {
		return StatusView{}, fmt.Errorf("starting the worker: %w", err)
	}
	if wait == 0 {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		view, err := c.Status()
		switch {
		case err == nil:
			return view, nil
		case !errors.Is(err, ErrNotRunning):
			return StatusView{}, err
		case time.Now().After(deadline):
			return StatusView{}, fmt.Errorf("the worker did not start within %v", wait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
