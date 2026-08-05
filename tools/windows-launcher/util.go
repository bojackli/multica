package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// logger writes to both the console and the log file so the user sees
// progress while a record remains for debugging.
type logger struct {
	mu   sync.Mutex
	file *os.File
}

func newLogger(path string) (*logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return &logger{file: f}, nil
}

func (l *logger) Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := time.Now().Format("2006-01-02 15:04:05") + "  " + msg + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Print(msg + "\n")
	if l.file != nil {
		_, _ = io.WriteString(l.file, line)
	}
}

func (l *logger) Close() {
	if l.file != nil {
		_ = l.file.Close()
	}
}

// findBinary looks up a command on PATH first, then in dir, and returns
// the absolute path that exists.
func findBinary(name string, dir string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("binary %q not found in PATH or %s", name, dir)
}

// commandPath returns the launcher-rooted path for a bundled binary.
func (c *Config) commandPath(rel string) string {
	return filepath.Join(c.Root, rel)
}
