package session_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/session"
)

// shortSocketPath avoids the ~100 character limit on unix socket paths, which t.TempDir often exceeds.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "shh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

type ipcRig struct {
	*workerRig
	path   string
	client *session.Client
	stop   context.CancelFunc
	done   chan error

	once   sync.Once
	result error
}

// shutdown stops the server and waits for Serve to return; it can be called more than once.
func (r *ipcRig) shutdown() error {
	r.once.Do(func() {
		r.stop()
		select {
		case r.result = <-r.done:
		case <-time.After(2 * time.Second):
			r.result = errors.New("the server did not stop")
		}
	})
	return r.result
}

func newIPCRig(t *testing.T) *ipcRig {
	t.Helper()
	r := &ipcRig{workerRig: newWorkerRig(t), path: shortSocketPath(t)}
	r.client = session.NewClient(r.path)
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	server, err := session.Listen(r.path, r.worker)
	if err != nil {
		t.Fatal(err)
	}
	r.done = make(chan error, 1)
	go func() { r.done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		if err := r.shutdown(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func TestEveryCommandChangesTheWorkerAndReturnsTheNewStatus(t *testing.T) {
	r := newIPCRig(t)
	c := r.client

	st, err := c.Status()
	if err != nil || st.Level != 0 || len(st.Layers) != 0 {
		t.Fatalf("Status = %+v, %v; want nothing active", st, err)
	}

	if st, err = c.Baseline(30); err != nil || st.Level != 30 || len(st.Layers) != 1 || st.Layers[0].Kind != "baseline" {
		t.Fatalf("Baseline = %+v, %v", st, err)
	}
	if !r.suppressor.Running() {
		t.Error("setting a baseline over IPC did not start the suppressor")
	}

	then := 0
	st, err = c.Focus(90, 50*time.Minute, &then, "deploy")
	top := st.Layers[len(st.Layers)-1]
	if err != nil || st.Level != 90 || len(st.Layers) != 2 || top.Note != "deploy" || top.RemainingSecs != 3000 || top.ThenLevel == nil || *top.ThenLevel != 0 || !top.Timed {
		t.Fatalf("Focus = %+v, %v", st, err)
	}

	if st, err = c.Adjust(5 * time.Minute); err != nil || st.Layers[1].RemainingSecs != 3300 {
		t.Fatalf("Adjust = %+v, %v; want 55 minutes left", st, err)
	}
	r.clock.Advance(10 * time.Minute)
	if st, err = c.Pause(); err != nil || !st.Layers[1].Paused || st.Layers[1].RemainingSecs != 2700 {
		t.Fatalf("Pause = %+v, %v; want paused with 45 minutes left", st, err)
	}
	r.clock.Advance(time.Hour)
	if st, _ = c.Status(); st.Layers[1].RemainingSecs != 2700 {
		t.Errorf("a paused timer kept counting: %d seconds left", st.Layers[1].RemainingSecs)
	}
	if st, err = c.Resume(); err != nil || st.Layers[1].Paused {
		t.Fatalf("Resume = %+v, %v", st, err)
	}
	if st, err = c.SetLevel(75); err != nil || st.Level != 75 || st.Layers[0].Percent != 30 {
		t.Fatalf("SetLevel = %+v, %v; want only the top layer changed", st, err)
	}
	if st, err = c.Stop(false); err != nil || st.Level != 30 || len(st.Layers) != 1 {
		t.Fatalf("Stop = %+v, %v; want the escalation gone and the baseline back", st, err)
	}
	if st, err = c.Stop(true); err != nil || st.Level != 0 || len(st.Layers) != 0 {
		t.Fatalf("Stop all = %+v, %v", st, err)
	}
	if r.suppressor.Running() {
		t.Error("stopping everything over IPC left the suppressor running")
	}
}

func TestStatusReflectsTheTimeLeft(t *testing.T) {
	r := newIPCRig(t)
	r.client.Focus(60, 30*time.Minute, nil, "")
	r.clock.Advance(12 * time.Minute)
	st, _ := r.client.Status()
	if got := st.Layers[0].RemainingSecs; got != 18*60 {
		t.Fatalf("remaining = %d seconds, want 1080", got)
	}
	if st.Layers[0].ExpiresAt == nil || !st.Layers[0].ExpiresAt.Equal(t0.Add(30*time.Minute)) {
		t.Errorf("expires at %v, want %v", st.Layers[0].ExpiresAt, t0.Add(30*time.Minute))
	}
}

func TestErrorsArriveAsTheirOriginalKinds(t *testing.T) {
	r := newIPCRig(t)
	c := r.client
	r.client.Baseline(30)

	tests := []struct {
		name string
		do   func() error
		want error
	}{
		{"stop with only a baseline", func() error { _, err := c.Stop(false); return err }, session.ErrNothingToStop},
		{"pause with no timer", func() error { _, err := c.Pause(); return err }, session.ErrNoTimer},
		{"resume with nothing paused", func() error { _, err := c.Resume(); return err }, session.ErrNotPaused},
		{"adjust with no timer", func() error { _, err := c.Adjust(time.Minute); return err }, session.ErrNoTimer},
		{"a level above 99", func() error { _, err := c.Baseline(100); return err }, session.ErrInvalid},
		{"a negative level", func() error { _, err := c.Focus(-1, 0, nil, ""); return err }, session.ErrInvalid},
		{"a negative duration", func() error { _, err := c.Focus(50, -time.Minute, nil, ""); return err }, session.ErrInvalid},
		{"a then level above 99", func() error { then := 100; _, err := c.Focus(50, 0, &then, ""); return err }, session.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.do(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want it to be %v", err, tt.want)
			}
		})
	}
	if st, _ := c.Status(); st.Level != 30 || len(st.Layers) != 1 {
		t.Errorf("rejected commands changed the state: %+v", st)
	}

	// With nothing active, changing the level has nothing to change.
	r.client.Stop(true)
	if _, err := c.SetLevel(50); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("SetLevel with nothing active: %v, want ErrNoSession", err)
	}
}

func TestSeveralClientsSeeTheSameWorker(t *testing.T) {
	r := newIPCRig(t)
	other := session.NewClient(r.path)
	if _, err := r.client.Baseline(45); err != nil {
		t.Fatal(err)
	}
	st, err := other.Status()
	if err != nil || st.Level != 45 {
		t.Fatalf("the second client sees %+v, %v", st, err)
	}
}

func TestConcurrentCommandsAreSafe(t *testing.T) {
	r := newIPCRig(t)
	if _, err := r.client.Focus(60, 5*time.Hour, nil, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := session.NewClient(r.path)
			if _, err := c.Adjust(time.Minute); err != nil {
				t.Error(err)
			}
			if _, err := c.Status(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	st, _ := r.client.Status()
	if got, want := st.Layers[0].RemainingSecs, 5*3600+20*60; got != want {
		t.Fatalf("remaining = %d, want %d: some adjustments were lost", got, want)
	}
}

func TestSubscribersReceiveBlockedAndAlertedEvents(t *testing.T) {
	r := newIPCRig(t)
	r.client.Baseline(65)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := r.client.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := session.NewClient(r.path).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	r.postAndTick(teams, "Ana", "prod outage, need you now")
	r.postAndTick(mail, "Weekly digest", "ten tips to boost your productivity, unsubscribe any time")

	for name, events := range map[string]<-chan session.Event{"first": a, "second": b} {
		alerted := receive(t, events, name)
		if alerted.Type != "decision" || alerted.Notification.Status != "alerted" || !alerted.Alerted || alerted.Notification.Source != "Teams" ||
			alerted.Notification.Sender != "Ana" || alerted.Notification.ID == 0 || alerted.Notification.Reason == "" {
			t.Errorf("%s subscriber, first event = %+v", name, alerted)
		}
		blocked := receive(t, events, name)
		if blocked.Notification.Status != "blocked" || blocked.Alerted || blocked.Notification.Source != "Mail" {
			t.Errorf("%s subscriber, second event = %+v", name, blocked)
		}
	}
}

func receive(t *testing.T, events <-chan session.Event, who string) session.Event {
	t.Helper()
	select {
	case e, ok := <-events:
		if !ok {
			t.Fatalf("%s subscriber: the stream closed", who)
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatalf("%s subscriber: no event", who)
		return session.Event{}
	}
}

func TestACancelledSubscriptionStopsAndDoesNotBlockTheWorker(t *testing.T) {
	r := newIPCRig(t)
	r.client.Baseline(30)
	ctx, cancel := context.WithCancel(context.Background())
	events, err := r.client.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-events:
		for ok { // drain anything in flight until it closes
			_, ok = <-events
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the event stream did not close after cancellation")
	}

	// A subscriber that never reads must not stall the pipeline.
	slow, err := r.client.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = slow
	for i := 0; i < 200; i++ {
		r.postAndTick(teams, "Ana", "can you send me your comments by Friday? number "+strings.Repeat("x", i%7)+string(rune('a'+i%26)))
		r.clock.Advance(15 * time.Second)
	}
	if got := len(r.notifications()); got < 150 {
		t.Fatalf("only %d notifications were processed; a slow subscriber stalled the worker", got)
	}
}

func TestWithNoWorkerTheErrorSaysSo(t *testing.T) {
	path := shortSocketPath(t)
	c := session.NewClient(path)
	for name, call := range map[string]func() error{
		"status":    func() error { _, err := c.Status(); return err },
		"baseline":  func() error { _, err := c.Baseline(30); return err },
		"subscribe": func() error { _, err := c.Subscribe(context.Background()); return err },
	} {
		err := call()
		if !errors.Is(err, session.ErrNotRunning) {
			t.Errorf("%s: err = %v, want ErrNotRunning", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), path) {
			t.Errorf("%s: %q should say where it looked", name, err)
		}
	}
}

func TestAStaleSocketFileIsCleanedUp(t *testing.T) {
	path := shortSocketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close() // leaves the socket file behind, as a crashed worker would
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: no stale socket: %v", err)
	}

	r := newWorkerRig(t)
	server, err := session.Listen(path, r.worker)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go server.Serve(ctx)
	defer cancel()
	if _, err := session.NewClient(path).Status(); err != nil {
		t.Fatalf("the new worker is not reachable: %v", err)
	}
}

func TestAnotherWorkerCannotTakeOverALiveSocket(t *testing.T) {
	r := newIPCRig(t)
	other := newWorkerRig(t)
	if _, err := session.Listen(r.path, other.worker); !errors.Is(err, session.ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
	if _, err := r.client.Status(); err != nil {
		t.Errorf("the running worker was disturbed: %v", err)
	}
}

func TestListenRefusesToDeleteAFileThatIsNotASocket(t *testing.T) {
	path := shortSocketPath(t)
	if err := os.WriteFile(path, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Listen(path, newWorkerRig(t).worker); err == nil {
		t.Fatal("Listen succeeded over a regular file")
	}
	if got, _ := os.ReadFile(path); string(got) != "precious" {
		t.Error("the regular file was damaged")
	}
}

func TestTheSocketIsPrivateAndRemovedOnShutdown(t *testing.T) {
	r := newIPCRig(t)
	info, err := os.Stat(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("socket mode = %o, want no access for group or others", perm)
	}

	events, err := r.client.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.shutdown(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if _, err := os.Stat(r.path); !os.IsNotExist(err) {
		t.Errorf("the socket file is still there after shutdown: %v", err)
	}
	if _, err := r.client.Status(); !errors.Is(err, session.ErrNotRunning) {
		t.Errorf("after shutdown: %v, want ErrNotRunning", err)
	}
	select {
	case _, ok := <-events:
		for ok {
			_, ok = <-events
		}
	case <-time.After(2 * time.Second):
		t.Error("a subscriber's stream stayed open after shutdown")
	}
}

func TestMalformedRequestsGetAnErrorAndTheConnectionSurvives(t *testing.T) {
	r := newIPCRig(t)
	conn, err := net.Dial("unix", r.path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewScanner(conn)
	ask := func(line string) map[string]any {
		t.Helper()
		if _, err := conn.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if !reader.Scan() {
			t.Fatalf("no response to %q: %v", line, reader.Err())
		}
		var out map[string]any
		if err := json.Unmarshal(reader.Bytes(), &out); err != nil {
			t.Fatalf("response %q is not JSON: %v", reader.Text(), err)
		}
		return out
	}

	if resp := ask(`this is not json`); resp["ok"] != false || resp["error"] == "" {
		t.Errorf("garbage got %v", resp)
	}
	if resp := ask(`{"cmd":"frobnicate"}`); resp["ok"] != false || !strings.Contains(resp["error"].(string), "frobnicate") {
		t.Errorf("an unknown command got %v", resp)
	}
	if resp := ask(`{"cmd":"baseline"}`); resp["ok"] != false {
		t.Errorf("a baseline with no level got %v", resp)
	}
	if resp := ask(`{"cmd":"status"}`); resp["ok"] != true {
		t.Errorf("a valid request after the bad ones got %v", resp)
	}
}

func TestAClientCanHaveTheWorkerShowAnAlert(t *testing.T) {
	r := newIPCRig(t)
	err := r.client.ShowAlert(alert.Alert{Kind: config.KindMessage, Source: "Teams", BundleID: "com.x", Title: "Ana", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Title != "Ana" || shown[0].Body != "hello" || shown[0].Source != "Teams" || shown[0].BundleID != "com.x" || shown[0].Kind != config.KindMessage {
		t.Errorf("worker showed %+v, want the alert the client sent", shown)
	}
}

func TestBadAlertRequestsAreRejected(t *testing.T) {
	r := newIPCRig(t)
	if err := r.client.ShowAlert(alert.Alert{Kind: "shout", Title: "x"}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("unknown kind: err = %v, want ErrInvalid", err)
	}
	if len(r.alerter.Shown()) != 0 {
		t.Error("a bad alert was shown")
	}
	r.alerter.FailWith(errors.New("display unavailable"))
	if err := r.client.ShowAlert(alert.Alert{Kind: config.KindCalendar, Title: "x"}); err == nil || !strings.Contains(err.Error(), "display unavailable") {
		t.Errorf("err = %v, want the alerter's error to reach the client", err)
	}
}

func TestAClientCanTrainAndUndoThroughTheWorker(t *testing.T) {
	r := newIPCRig(t)
	if err := r.worker.SetBaseline(65); err != nil {
		t.Fatal(err)
	}
	r.postAndTick(teams, "Ana", "lunch menu for friday")
	id := r.onlyNotification().ID
	label := 90
	got, err := r.client.Train(session.TrainRequest{NotificationID: id, Label: &label})
	if err != nil || got.NotificationID != id || got.After <= got.Before || !got.Updated {
		t.Fatalf("Train = %+v, %v", got, err)
	}
	if row, _ := r.store.GetNotification(id); !row.IsTrained {
		t.Error("the row is not marked trained")
	}
	if _, err := r.client.Train(session.TrainRequest{NotificationID: id, Feedback: "y"}); !errors.Is(err, session.ErrAlreadyTrained) {
		t.Errorf("a second train: err = %v, want ErrAlreadyTrained across the socket", err)
	}
	undone, err := r.client.UndoTraining()
	if err != nil || undone.NotificationID != id {
		t.Fatalf("UndoTraining = %+v, %v", undone, err)
	}
	if row, _ := r.store.GetNotification(id); row.IsTrained {
		t.Error("the row is still trained after undo")
	}
	if _, err := r.client.UndoTraining(); !errors.Is(err, session.ErrNothingToUndo) {
		t.Errorf("a second undo: err = %v", err)
	}
	if _, err := r.client.Train(session.TrainRequest{NotificationID: 9999, Feedback: "y"}); !errors.Is(err, session.ErrNoSuchNotification) {
		t.Errorf("an unknown id: err = %v", err)
	}
	if _, err := r.client.Train(session.TrainRequest{NotificationID: id}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("no label: err = %v", err)
	}
}
