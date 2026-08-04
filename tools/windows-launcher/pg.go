package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// pgBinaries returns the portable PG tool paths. On Windows these live in
// postgres\bin\*.exe; they are shelled out because embedding PostgreSQL is
// out of scope for the launcher.
func (c *Config) pgTool(name string) string {
	exe := name
	if isWindows() {
		exe += ".exe"
	}
	return filepath.Join(c.PGBin, exe)
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}

// pgDataInitialized reports whether the cluster has been initdb'd.
func (c *Config) pgDataInitialized() bool {
	_, err := os.Stat(filepath.Join(c.PGData, "PG_VERSION"))
	return err == nil
}

// initPGCluster runs initdb to create a fresh cluster owned by PGUser.
func (c *Config) initPGCluster(log *logger) error {
	log.Logf("Initializing PostgreSQL data directory: %s", c.PGData)
	if err := os.MkdirAll(c.PGData, 0o755); err != nil {
		return fmt.Errorf("create pgdata dir: %w", err)
	}

	// initdb needs a password file for -A scram-sha-256 with a password.
	pwFile := filepath.Join(os.TempDir(), "multica_pg_pw")
	if err := os.WriteFile(pwFile, []byte(c.PGPassword), 0o600); err != nil {
		return fmt.Errorf("write temp password file: %w", err)
	}
	defer os.Remove(pwFile)

	args := []string{
		"-D", c.PGData,
		"-U", c.PGUser,
		"-A", "scram-sha-256",
		"--pwfile=" + pwFile,
		"-E", "UTF8",
	}
	cmd := exec.Command(c.pgTool("initdb"), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initdb failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	log.Logf("PostgreSQL initialized.")
	return nil
}

// startPG launches the postmaster detached and waits for it to accept
// connections on the configured port.
func (c *Config) startPG(log *logger) error {
	if !c.pgDataInitialized() {
		if err := c.initPGCluster(log); err != nil {
			return err
		}
	}

	log.Logf("Starting PostgreSQL on 127.0.0.1:%d", c.PGPort)
	// Note: no "-w" here. On Windows pg_ctl -w probes the server via
	// "localhost", which resolves to ::1, while the server only listens on
	// 127.0.0.1 (IPv4) — so pg_ctl -w hangs forever waiting on a server it
	// never sees as ready. We instead wait below with waitForPort, which
	// dials 127.0.0.1 directly over IPv4.
	args := []string{
		"start",
		"-D", c.PGData,
		"-l", c.PGLog,
		"-o", fmt.Sprintf("-p %d -h 127.0.0.1", c.PGPort),
	}
	cmd := exec.Command(c.pgTool("pg_ctl"), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_ctl start failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	if err := c.waitForPort("127.0.0.1", c.PGPort, 60*time.Second); err != nil {
		return fmt.Errorf("PostgreSQL did not become ready: %w", err)
	}
	log.Logf("PostgreSQL ready.")
	return nil
}

// stopPG shuts the cluster down with pg_ctl stop (fast).
func (c *Config) stopPG(log *logger) error {
	if !c.pgDataInitialized() {
		return nil
	}
	log.Logf("Stopping PostgreSQL...")
	cmd := exec.Command(c.pgTool("pg_ctl"), "stop", "-D", c.PGData, "-m", "fast", "-w")
	if out, err := cmd.CombinedOutput(); err != nil {
		// A cluster that is already stopped is fine.
		if !strings.Contains(string(out), "PID file") {
			log.Logf("pg_ctl stop: %s", strings.TrimSpace(string(out)))
		}
		return err
	}
	log.Logf("PostgreSQL stopped.")
	return nil
}

// ensureDatabase creates the application database if it does not exist.
func (c *Config) ensureDatabase(log *logger) error {
	exists, err := c.databaseExists()
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	log.Logf("Creating database %q", c.PGDatabase)
	env := c.pgEnv()
	args := []string{"-h", "127.0.0.1", "-p", fmt.Sprint(c.PGPort), "-U", c.PGUser, c.PGDatabase}
	cmd := exec.Command(c.pgTool("createdb"), args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("createdb failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	log.Logf("Database %q created.", c.PGDatabase)
	return nil
}

func (c *Config) databaseExists() (bool, error) {
	env := c.pgEnv()
	cmd := exec.Command(c.pgTool("psql"), "-h", "127.0.0.1", "-p", fmt.Sprint(c.PGPort), "-U", c.PGUser, "-d", "postgres", "-tAc", "SELECT 1 FROM pg_database WHERE datname = '"+c.PGDatabase+"'")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("psql probe failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)) == "1", nil
}

// pgEnv exports PGPASSWORD so child tools authenticate.
func (c *Config) pgEnv() []string {
	return append(os.Environ(), "PGPASSWORD="+c.PGPassword)
}

// waitForPort polls a TCP endpoint until it accepts connections or times out.
func (c *Config) waitForPort(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("%s:%d", host, port)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}
