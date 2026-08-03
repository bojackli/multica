package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// State records what the launcher started so StopMultica.exe can tear it
// down in the right order even from a separate process.
type State struct {
	BackendPid int    `json:"backend_pid"`
	ClientPid  int    `json:"client_pid"`
	PGPort     int    `json:"pg_port"`
	PGData     string `json:"pg_data"`
	StartedAt  string `json:"started_at"`
}

func (c *Config) loadState() (*State, error) {
	raw, err := os.ReadFile(c.StateFile)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}
	return &s, nil
}

func (c *Config) saveState(s *State) error {
	s.StartedAt = time.Now().Format(time.RFC3339)
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.StateFile, raw, 0o644)
}

func (c *Config) clearState() {
	_ = os.Remove(c.StateFile)
}

// isProcessAlive checks whether a pid is still running (best effort).
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if isWindows() {
		// tasklist /FI "PID eq N" exits 0 when the process exists.
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return false
		}
		// A non-header line containing the pid means it is running.
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), strconv.Itoa(pid)) {
				return true
			}
		}
		return false
	}
	// Unix: signal 0 probe.
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	return cmd.Run() == nil
}

// stopProcess terminates a process, escalating to force-kill if needed.
func stopProcess(pid int, log *logger) {
	if !isProcessAlive(pid) {
		return
	}
	log.Logf("Stopping process %d", pid)
	if isWindows() {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
		return
	}
	_ = exec.Command("kill", strconv.Itoa(pid)).Run()
	time.Sleep(2 * time.Second)
	if isProcessAlive(pid) {
		_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}
}
