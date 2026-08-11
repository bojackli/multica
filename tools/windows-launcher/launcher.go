package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// start launches the full stack: PG -> migrate -> backend -> client (and
// optionally the agent daemon). It blocks until the client exits or a stop
// flag is raised, then tears everything down.
func start(c *Config, log *logger) error {
	log.Logf("=== Multica launcher starting ===")
	log.Logf("Root: %s", c.Root)

	if _, err := os.Stat(c.ServerExe); err != nil {
		return fmt.Errorf("backend not found at %s — run the pack script first", c.ServerExe)
	}
	if _, err := os.Stat(c.MigrateExe); err != nil {
		return fmt.Errorf("migrate tool not found at %s", c.MigrateExe)
	}
	if _, err := os.Stat(c.pgTool("pg_ctl")); err != nil {
		return fmt.Errorf("portable PostgreSQL not found in %s", c.PGBin)
	}

	// 1. PostgreSQL
	if err := c.startPG(log); err != nil {
		return err
	}
	defer func() {
		_ = c.stopPG(log)
	}()

	// 2. Application database
	if err := c.ensureDatabase(log); err != nil {
		return err
	}

	// 3. Migrations
	if err := c.runMigrations(log); err != nil {
		return err
	}

	// 4. Backend
	backendCmd, err := c.startBackend(log)
	if err != nil {
		return err
	}
	defer stopProcess(backendCmd.Process.Pid, log)

	if err := c.waitForHTTP("http://127.0.0.1:"+fmt.Sprint(c.ServerPort)+"/health", 60*time.Second); err != nil {
		return fmt.Errorf("backend did not become healthy: %w", err)
	}
	log.Logf("Backend ready at http://127.0.0.1:%d", c.ServerPort)

	// 5. Electron client
	clientCmd, err := c.startClient(log)
	if err != nil {
		return err
	}

	// 6. Agent daemon
	var agentCmd *exec.Cmd
	if c.EnableAgent {
		if cmd, err := c.startAgentDaemon(log); err == nil {
			agentCmd = cmd
		} else {
			log.Logf("Agent daemon skipped: %v", err)
		}
	}

	state := &State{
		BackendPid: backendCmd.Process.Pid,
		PGPort:     c.PGPort,
		PGData:     c.PGData,
	}
	if clientCmd != nil && clientCmd.Process != nil {
		state.ClientPid = clientCmd.Process.Pid
	}
	_ = c.saveState(state)

	// 7. Wait: until the client exits, the stop flag appears, or a manual
	// shutdown is requested. The client closing its window is the normal
	// "I'm done" signal.
	waitErr := waitForShutdown(c, clientCmd, agentCmd, log)

	log.Logf("Shutting down Multica stack...")
	if agentCmd != nil {
		stopProcess(agentCmd.Process.Pid, log)
	}
	if clientCmd != nil {
		stopProcess(clientCmd.Process.Pid, log)
	}
	stopProcess(backendCmd.Process.Pid, log)
	_ = c.stopPG(log)
	c.clearState()
	log.Logf("=== Multica stopped ===")
	return waitErr
}

// waitForShutdown returns when the stop flag appears, or the client exits.
func waitForShutdown(c *Config, clientCmd *exec.Cmd, agentCmd *exec.Cmd, log *logger) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := os.Stat(c.StopFile); err == nil {
			_ = os.Remove(c.StopFile)
			log.Logf("Stop flag detected.")
			return nil
		}
		if clientCmd != nil && clientCmd.Process != nil && !isProcessAlive(clientCmd.Process.Pid) {
			log.Logf("Client process exited.")
			return nil
		}
	}
	return nil
}

// runMigrations invokes migrate.exe up with DATABASE_URL set.
func (c *Config) runMigrations(log *logger) error {
	log.Logf("Running database migrations...")
	env := append(os.Environ(), "DATABASE_URL="+c.DatabaseURL)
	cmd := exec.Command(c.MigrateExe, "up")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("migrations failed: %w", err)
	}
	log.Logf("Migrations complete.")
	return nil
}

// startBackend launches server.exe detached with the right env.
func (c *Config) startBackend(log *logger) (*exec.Cmd, error) {
	log.Logf("Starting backend on port %d...", c.ServerPort)

	secret, err := c.loadOrCreateJWTSecret()
	if err != nil {
		return nil, err
	}

	env := append(os.Environ(),
		"DATABASE_URL="+c.DatabaseURL,
		"JWT_SECRET="+secret,
		"PORT="+fmt.Sprint(c.ServerPort),
		"APP_ENV="+c.AppEnv,
		"MULTICA_DEV_VERIFICATION_CODE="+c.VerifyCode,
		"ALLOW_SIGNUP=true",
		"FRONTEND_ORIGIN="+c.FrontendOrigin,
		"CORS_ALLOWED_ORIGINS="+c.FrontendOrigin,
		"MULTICA_APP_URL="+c.FrontendOrigin,
		"LOCAL_UPLOAD_DIR="+c.UploadsDir,
	)
	cmd := exec.Command(c.ServerExe)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start backend: %w", err)
	}
	log.Logf("Backend started (pid %d).", cmd.Process.Pid)
	return cmd, nil
}

// loadOrCreateJWTSecret reads a persisted secret or generates one.
func (c *Config) loadOrCreateJWTSecret() (string, error) {
	if raw, err := os.ReadFile(c.JWTSecretFile); err == nil && len(raw) >= 16 {
		return string(raw), nil
	}
	secret := randomHex(32)
	if err := os.WriteFile(c.JWTSecretFile, []byte(secret), 0o600); err != nil {
		return "", fmt.Errorf("write jwt secret: %w", err)
	}
	return secret, nil
}

// startClient launches the Electron client if present. In packaged (non-dev)
// builds the client reads its backend URL from ~/.multica/desktop.json, so we
// write that file first to point it at the local backend. This avoids
// rebuilding the client for single-machine use.
func (c *Config) startClient(log *logger) (*exec.Cmd, error) {
	if _, err := os.Stat(c.ClientExe); err != nil {
		log.Logf("Electron client not found at %s — skipping.", c.ClientExe)
		return nil, nil
	}
	if err := c.writeClientConfig(log); err != nil {
		log.Logf("Warning: could not write client config: %v", err)
	}
	log.Logf("Starting Electron client...")
	cmd := exec.Command(c.ClientExe)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start client: %w", err)
	}
	log.Logf("Client started (pid %d).", cmd.Process.Pid)
	return cmd, nil
}

// writeClientConfig ensures ~/.multica/desktop.json points the packaged
// Electron client at the local backend.
func (c *Config) writeClientConfig(log *logger) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".multica")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	cfg := map[string]any{
		"schemaVersion": 1,
		"apiUrl":        "http://127.0.0.1:" + fmt.Sprint(c.ServerPort),
		"wsUrl":         "ws://127.0.0.1:" + fmt.Sprint(c.ServerPort) + "/ws",
		"appUrl":        c.FrontendOrigin,
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "desktop.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	log.Logf("Wrote client config: %s", path)
	return nil
}

// startAgentDaemon launches `multica daemon start` so local agents can work.
func (c *Config) startAgentDaemon(log *logger) (*exec.Cmd, error) {
	if _, err := os.Stat(c.AgentExe); err != nil {
		return nil, fmt.Errorf("multica CLI not found at %s", c.AgentExe)
	}
	log.Logf("Starting agent daemon...")
	env := append(os.Environ(),
		"MULTICA_SERVER_URL=ws://127.0.0.1:"+fmt.Sprint(c.ServerPort)+"/ws",
		"MULTICA_APP_URL="+c.FrontendOrigin,
	)
	cmd := exec.Command(c.AgentExe, "daemon", "start")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent daemon: %w", err)
	}
	log.Logf("Agent daemon started (pid %d).", cmd.Process.Pid)
	return cmd, nil
}

// waitForHTTP polls a URL until it returns 200 or times out.
func (c *Config) waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}
