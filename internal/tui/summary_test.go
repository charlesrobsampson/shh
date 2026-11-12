package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charlesrobsampson/shh/internal/store"
)

// summarizeKey presses s and delivers the answer of the command it started, as the program would.
func (r *rig) summarizeKey() {
	r.t.Helper()
	_, cmd := r.m.Update(key("s"))
	if cmd == nil {
		return
	}
	for _, msg := range flatten(cmd()) {
		r.m.Update(msg)
	}
}

func flatten(msg tea.Msg) []tea.Msg {
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			if c != nil {
				out = append(out, flatten(c())...)
			}
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestSAsksForASummaryShowsItAndKeepsItWithoutChangingTheNotification(t *testing.T) {
	r := newRig(t)
	id := r.add("Ana", "can you look at the deploy before 3?", 5)
	r.refresh()
	calls := 0
	r.m.cfg.SummarizerName = "ollama"
	r.m.cfg.Summarize = func(n store.Notification) (string, error) {
		calls++
		return "Ana asks for a deploy check.", r.store.SetSummary(n.ID, "Ana asks for a deploy check.")
	}
	r.summarizeKey()
	if calls != 1 || !strings.Contains(r.view(), "summary (ollama): Ana asks for a deploy check.") {
		t.Fatalf("calls %d view:\n%s", calls, r.view())
	}
	got := r.row(id)
	if got.IsRead || got.IsTrained || got.IsDismissedInbox || got.Summary != "Ana asks for a deploy check." {
		t.Errorf("only the summary changes: %+v", got)
	}
	r.summarizeKey()
	if calls != 1 || !strings.Contains(r.messageLine(), "already summarized") {
		t.Errorf("a summary is made once: calls %d, message %q", calls, r.messageLine())
	}
}

func TestSWithNoBackendSaysHowToSetOneUp(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "hello", 5)
	r.refresh()
	r.summarizeKey()
	if !strings.Contains(r.messageLine(), "no summarizer is set up") {
		t.Errorf("message %q", r.messageLine())
	}
	r.m.cfg.Summarize = func(store.Notification) (string, error) { return "", ErrNoSummarizer }
	r.m.notice = ""
	r.summarizeKey()
	if !strings.Contains(r.messageLine(), "summarizer.backend") && !strings.Contains(r.messageLine(), "backend") {
		t.Errorf("message %q", r.messageLine())
	}
}

func TestABackendFailureIsAMessageNotACrash(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "hello", 5)
	r.refresh()
	r.m.cfg.Summarize = func(store.Notification) (string, error) {
		return "", errors.New("ollama (is it running? ollama serve): connection refused")
	}
	r.summarizeKey()
	if !strings.Contains(r.messageLine(), "could not summarize") || strings.Contains(r.view(), "summary (") {
		t.Errorf("message %q", r.messageLine())
	}
	// and it can be tried again
	r.m.cfg.Summarize = func(store.Notification) (string, error) { return "fine", nil }
	r.m.notice = ""
	r.summarizeKey()
	if !strings.Contains(r.view(), "fine") {
		t.Errorf("view:\n%s", r.view())
	}
}

func TestTheStatusBarSaysWhenSummariesLeaveTheMac(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "hello", 5)
	r.refresh()
	if strings.Contains(r.view(), "leaves this Mac") {
		t.Error("nothing leaves the Mac with no remote backend")
	}
	r.m.cfg.SummarizerName, r.m.cfg.SummarizerRemote = "claude", true
	r.m.width = 160
	if !strings.Contains(r.view(), "claude summaries: text leaves this Mac") {
		t.Errorf("view:\n%s", r.view())
	}
}

func TestSOnTheFocusTabStillMeansStart(t *testing.T) {
	r := newRig(t)
	r.add("Ana", "hello", 5)
	r.m.cfg.Summarize = func(store.Notification) (string, error) {
		t.Error("summarize must not run on the Focus tab")
		return "", nil
	}
	r.press("f1", "s")
}
