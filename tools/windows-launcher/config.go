package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Config holds every path and value the launcher needs. All paths are
// derived from the launcher executable's own directory, so the whole
// deployment stays relocatable.
type Config struct {
	// Root is the directory containing the launcher exe.
	Root string

	// Postgres is the portable PostgreSQL 17 installation (contains bin/).
	PostgresDir string
	// PGBin holds the portable PG binaries.
	PGBin string
	// PGData is the cluster data directory (initdb target).
	PGData string
	// PGLog is where postmaster output is appended.
	PGLog string
	// PGPort is the postmaster listen port. Not 5432 so a system PG on the
	// default port does not collide.
	PGPort int

	// DB credentials for the dedicated `multica` role + database.
	PGUser     string
	PGPassword string
	PGDatabase string

	// Server binary + how the backend reaches the DB.
	ServerExe   string
	MigrateExe  string
	ClientExe   string
	DatabaseURL string

	// Backend listen port.
	ServerPort int

	// JWT secret for the backend; generated once and persisted.
	JWTSecretFile string

	// Runtime state files written next to the exe.
	StateFile    string // pid + role info for the running stack
	StopFile     string // presence triggers shutdown from StopMultica.exe
	BackendPid   string // backend process id (internal)
	ClientPid    string // electron client process id (internal)
	JWTSecret    string // loaded or generated
	LogFile      string // launcher console log
	EnableAgent  bool   // start multica daemon after backend is healthy
	AgentExe     string // multica CLI binary (daemon)
	UploadsDir   string // LOCAL_UPLOAD_DIR for the backend
	AppEnv       string // APP_ENV for the backend
	VerifyCode   string // MULTICA_DEV_VERIFICATION_CODE
	FrontendOrigin string // CORS origin for the electron client
}

// defaultConfig derives all paths from the launcher's own location.
func defaultConfig() (*Config, error) {
	root, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve launcher path: %w", err)
	}
	root = filepath.Dir(root)

	dataDir := filepath.Join(root, "data")

	c := &Config{
		Root:         root,
		PostgresDir:  filepath.Join(root, "postgres"),
		PGBin:        filepath.Join(root, "postgres", "bin"),
		PGData:       filepath.Join(dataDir, "pgdata"),
		PGLog:        filepath.Join(dataDir, "postgres.log"),
		PGPort:       5433,
		PGUser:       "multica",
		PGPassword:   "multica",
		PGDatabase:   "multica",
		ServerExe:    filepath.Join(root, "server.exe"),
		MigrateExe:   filepath.Join(root, "migrate.exe"),
		ClientExe:    filepath.Join(root, "client", "Multica.exe"),
		AgentExe:     filepath.Join(root, "multica.exe"),
		ServerPort:   8080,
		JWTSecretFile: filepath.Join(dataDir, "jwt_secret"),
		StateFile:    filepath.Join(dataDir, "multica.state"),
		StopFile:     filepath.Join(dataDir, "stop.flag"),
		LogFile:      filepath.Join(dataDir, "launcher.log"),
		UploadsDir:   filepath.Join(dataDir, "uploads"),
		AppEnv:       "development",
		VerifyCode:   "888888",
		FrontendOrigin: "http://127.0.0.1:8080",
		EnableAgent:  true,
	}

	c.DatabaseURL = fmt.Sprintf(
		"postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		c.PGUser, c.PGPassword, c.PGPort, c.PGDatabase,
	)

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(c.UploadsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create uploads dir: %w", err)
	}

	return c, nil
}
