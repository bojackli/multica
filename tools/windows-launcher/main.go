package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// The launcher behaves by which copy it is invoked as. When installed as
	// StopMultica.exe it stops; as StartMultica.exe (or any other name) it
	// starts. An explicit subcommand wins over the filename.
	action := "start"
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.HasPrefix(base, "stop") {
		action = "stop"
	}

	fs := flag.NewFlagSet("multica-launcher", flag.ExitOnError)
	fs.StringVar(&action, "action", action, "start or stop")
	_ = fs.Parse(os.Args[1:])

	cfg, err := defaultConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher config error:", err)
		os.Exit(1)
	}

	log, err := newLogger(cfg.LogFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "log error:", err)
		os.Exit(1)
	}
	defer log.Close()

	var runErr error
	switch action {
	case "start":
		runErr = start(cfg, log)
	case "stop":
		runErr = stop(cfg, log)
	default:
		fmt.Fprintln(os.Stderr, "unknown action:", action)
		os.Exit(2)
	}

	if runErr != nil {
		log.Logf("Error: %v", runErr)
		os.Exit(1)
	}
}
