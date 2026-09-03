package monkeyscode

import (
	"errors"
	"net/http"
)

// Client is the main entry point for the MonkeysCode Go SDK.
type Client struct {
	config     *Config
	httpClient *http.Client
}

// NewClient creates a new MonkeysCode client.
//
// If cfg is nil, DefaultConfig() is used. The API key is required —
// either set it directly or via MONKEYSCODE_API_KEY env var.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	if cfg.APIKey == "" {
		return nil, errors.New("monkeyscode: API key is required (set Config.APIKey or MONKEYSCODE_API_KEY)")
	}

	if cfg.ProxyURL == "" {
		cfg.ProxyURL = "https://api.monkeyscode.com"
	}

	return &Client{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}, nil
}

// Agent creates a new Agent bound to a workspace directory.
// If workspace is empty, Config.WorkingDirectory is used.
func (c *Client) Agent(workspace string) *Agent {
	if workspace == "" {
		workspace = c.config.WorkingDirectory
	}
	return &Agent{
		client:    c,
		workspace: workspace,
	}
}

// Config returns a copy of the client's current configuration.
func (c *Client) Config() Config {
	return *c.config
}
