package main

import (
	"os"
)

// stop tears the stack down using the persisted state. It works even if the
// original StartMultica.exe is no longer running, and always attempts to stop
// PostgreSQL so a crashed start cannot leave a stray cluster behind.
func stop(c *Config, log *logger) error {
	log.Logf("=== Multica stop requested ===")

	if state, err := c.loadState(); err != nil {
		log.Logf("No running stack state found (%v).", err)
	} else {
		if state.ClientPid > 0 {
			log.Logf("Stopping Electron client (pid %d)...", state.ClientPid)
			stopProcess(state.ClientPid, log)
		}
		if state.BackendPid > 0 {
			log.Logf("Stopping backend (pid %d)...", state.BackendPid)
			stopProcess(state.BackendPid, log)
		}
	}

	// Always try to stop PostgreSQL; if the start previously crashed before
	// writing state, this cleans up the stray cluster.
	if err := c.stopPG(log); err != nil {
		log.Logf("Error stopping PostgreSQL: %v", err)
	}

	c.clearState()
	_ = os.Remove(c.StopFile)
	log.Logf("=== Multica stopped ===")
	return nil
}
