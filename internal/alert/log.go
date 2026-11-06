package alert

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Log is an Alerter that only writes a line to a writer. It stands in until a banner alerter is wired up, and is handy for
// running the worker headless.
type Log struct {
	mu sync.Mutex
	w  io.Writer
}

func NewLog(w io.Writer) *Log { return &Log{w: w} }

func (l *Log) Show(a Alert) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	body := strings.Join(strings.Fields(a.Body), " ")
	_, err := fmt.Fprintf(l.w, "%s ALERT [%s] %s: %s - %s\n", time.Now().Format("15:04:05"), a.Kind, a.Source, a.Title, body)
	return err
}
