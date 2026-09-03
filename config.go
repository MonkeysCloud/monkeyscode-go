package monkeyscode

import (
	"os"
	"time"
)

// Config configures the MonkeysCode agent client.
type Config struct {
	// APIKey for authentication. Falls back to MONKEYSCODE_API_KEY env var.
	APIKey string

	// ProxyURL is the model-proxy endpoint. Defaults to https://api.monkeyscode.com.
	ProxyURL string

	// Model to use (e.g., "capuchin-reason", "capuchin-flash"). Defaults to "capuchin-reason".
	Model string

	// Maximum time to wait for a single run to complete. Defaults to 5 minutes.
	Timeout time.Duration

	// Enable sandbox mode for safe execution. Defaults to true.
	Sandboxed bool

	// Maximum number of agent turns per run. Defaults to 50.
	MaxTurns int

	// Working directory for the agent. Defaults to current directory.
	WorkingDirectory string

	// Permissions mode: "auto-approve", "suggest", "strict". Defaults to "auto-approve".
	PermissionsMode string

	// Maximum cost per run in USD. 0 means unlimited.
	MaxCost float64
}

// DefaultConfig returns a Config with sensible defaults.
// APIKey is read from MONKEYSCODE_API_KEY if not set.
func DefaultConfig() *Config {
	wd, _ := os.Getwd()
	return &Config{
		APIKey:           os.Getenv("MONKEYSCODE_API_KEY"),
		ProxyURL:         "https://api.monkeyscode.com",
		Model:            "capuchin-reason",
		Timeout:          5 * time.Minute,
		Sandboxed:        true,
		MaxTurns:         50,
		WorkingDirectory: wd,
		PermissionsMode:  "auto-approve",
	}
}
