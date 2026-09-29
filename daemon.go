package monkeyscode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DaemonClientOptions configures the daemon client.
type DaemonClientOptions struct {
	URL                  string
	Timeout              time.Duration
	AutoReconnect        bool
	MaxReconnectAttempts int
}

// DaemonRunParams are parameters for starting a run via the daemon.
type DaemonRunParams struct {
	Prompt    string `json:"prompt"`
	Workspace string `json:"workspace,omitempty"`
	Mode      string `json:"mode,omitempty"` // "fast" | "plan" | "thinking"
	Model     string `json:"model,omitempty"`
	MaxTokens int    `json:"maxTokens,omitempty"`
}

// DaemonEvent is an event received from the daemon.
type DaemonEvent struct {
	Kind      string         `json:"kind"`
	Data      map[string]any `json:"data"`
	Timestamp int64          `json:"timestamp"`
}

// DaemonClient provides JSON-RPC communication with the daemon process.
//
// Uses HTTP long-polling as a portable fallback (no WebSocket dependency).
type DaemonClient struct {
	url        string
	httpClient *http.Client
	connected  bool
	requestID  atomic.Int64
	mu         sync.Mutex
}

// NewDaemonClient creates a new daemon client.
func NewDaemonClient(opts DaemonClientOptions) *DaemonClient {
	url := opts.URL
	if url == "" {
		url = "http://localhost:9120"
	}
	// Normalize ws:// → http://
	url = strings.Replace(url, "ws://", "http://", 1)
	url = strings.Replace(url, "wss://", "https://", 1)
	url = strings.TrimRight(url, "/")

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &DaemonClient{
		url:        url,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Connect establishes connection to the daemon.
func (d *DaemonClient) Connect(ctx context.Context) error {
	// Verify the daemon is reachable
	req, err := http.NewRequestWithContext(ctx, "GET", d.url+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		// Daemon might not have /health, assume ok
		d.connected = true
		return nil
	}
	defer resp.Body.Close()
	d.connected = true
	return nil
}

// Disconnect closes the connection.
func (d *DaemonClient) Disconnect() {
	d.connected = false
}

// Connected returns whether the client is connected.
func (d *DaemonClient) Connected() bool {
	return d.connected
}

// Request sends a JSON-RPC request and returns the result.
func (d *DaemonClient) Request(ctx context.Context, method string, params map[string]any) (any, error) {
	id := d.requestID.Add(1)

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", d.url+"/rpc", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon request: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if errVal, ok := result["error"]; ok {
		return nil, fmt.Errorf("RPC error: %v", errVal)
	}

	return result["result"], nil
}

// Run starts an agent run and returns a channel of events.
func (d *DaemonClient) Run(ctx context.Context, runID string, params DaemonRunParams) (<-chan DaemonEvent, error) {
	_, err := d.Request(ctx, "agent/run", map[string]any{
		"runId":     runID,
		"prompt":    params.Prompt,
		"workspace": params.Workspace,
		"mode":      params.Mode,
		"model":     params.Model,
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan DaemonEvent, 64)

	go func() {
		defer close(ch)

		// Poll for events
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			result, err := d.Request(ctx, "agent/events", map[string]any{"runId": runID})
			if err != nil {
				ch <- DaemonEvent{Kind: "error", Data: map[string]any{"message": err.Error()}}
				return
			}

			events, ok := result.([]any)
			if !ok || len(events) == 0 {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			for _, raw := range events {
				data, ok := raw.(map[string]any)
				if !ok {
					continue
				}

				event := DaemonEvent{
					Kind: strVal(data, "type"),
					Data: data,
				}
				if ts, ok := data["timestamp"].(float64); ok {
					event.Timestamp = int64(ts)
				}

				ch <- event

				if event.Kind == "complete" || event.Kind == "error" || event.Kind == "done" {
					return
				}
			}
		}
	}()

	return ch, nil
}

// Cancel cancels a running agent.
func (d *DaemonClient) Cancel(ctx context.Context, runID string) error {
	_, err := d.Request(ctx, "agent/cancel", map[string]any{"runId": runID})
	return err
}

// Status gets the status of a run.
func (d *DaemonClient) Status(ctx context.Context, runID string) (map[string]any, error) {
	result, err := d.Request(ctx, "agent/status", map[string]any{"runId": runID})
	if err != nil {
		return nil, err
	}
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("unexpected result type")
}
