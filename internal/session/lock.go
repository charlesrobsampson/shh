package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lock is an exclusive advisory lock on a file. The operating system drops it when the process dies, so a crashed daemon can
// never leave a lock behind, unlike a PID file.
type lock struct {
	file *os.File
}

func acquireLock(path string) (*lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (lock %s is held)", ErrAlreadyRunning, path)
		}
		return nil, err
	}
	return &lock{file: f}, nil
}

func (l *lock) release() {
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
}

// writePID records the daemon's process id for people and tools such as `ps`; the lock, not this file, decides who is running.
func writePID(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

// removePID deletes the PID file only if it is still ours.
func removePID(path string) {
	raw, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(raw)) == strconv.Itoa(os.Getpid()) {
		os.Remove(path)
	}
}
