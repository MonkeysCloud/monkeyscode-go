// Package monkeyscode provides a Go client for the MonkeysCode AI agent API.
//
// Usage:
//
//	client := monkeyscode.NewClient(monkeyscode.Options{
//	    APIKey: os.Getenv("MONKEYSCODE_API_KEY"),
//	    Model:  "capuchin-reason",
//	})
//
//	result, err := client.Run(ctx, "Fix all Go lint errors")
//	fmt.Println(result.Summary)
//	fmt.Printf("Cost: $%.4f\n", result.Cost)
package monkeyscode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultProxyURL is the default model proxy endpoint.
	DefaultProxyURL = "https://models.monkeyscode.com"

	// DefaultMaxTurns is the default maximum turns per run.
	DefaultMaxTurns = 30

	// DefaultTimeout is the default timeout.
	DefaultTimeout = 5 * time.Minute
)

// Options configures a MonkeysCode client.
type Options struct {
	// APIKey for authentication. Falls back to MONKEYSCODE_API_KEY env var.
	APIKey string

	// Model to use (default: "auto").
	Model string

	// ProxyURL overrides the model proxy endpoint.
	ProxyURL string

	// WorkingDirectory for the agent (default: cwd).
	WorkingDirectory string

	// MaxTurns per run (default: 30).
	MaxTurns int

	// Timeout for the entire run (default: 5m).
	Timeout time.Duration

	// PermissionsMode: "ask", "acceptEdits", "autoApprove" (default: "ask").
	PermissionsMode string

	// MaxCost in USD. Zero means unlimited.
	MaxCost float64

	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client

	// Debug enables debug logging to stderr.
	Debug bool
}

// Client is the main MonkeysCode SDK client.
type Client struct {
	apiKey     string
	model      string
	proxyURL   string
	workDir    string
	maxTurns   int
	timeout    time.Duration
	permMode   string
	maxCost    float64
	httpClient *http.Client
	debug      bool

	totalCost   float64
	totalTokens TokenUsage
	sessionID   string
	tools       *ToolRegistry
	hooks       *HookManager
}

// NewClient creates a new MonkeysCode client.
func NewClient(opts Options) *Client {
	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("MONKEYSCODE_API_KEY")
	}

	model := opts.Model
	if model == "" {
		model = "auto"
	}

	proxyURL := opts.ProxyURL
	if proxyURL == "" {
		proxyURL = os.Getenv("MONKEYSCODE_PROXY_URL")
		if proxyURL == "" {
			proxyURL = DefaultProxyURL
		}
	}

	workDir := opts.WorkingDirectory
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	maxTurns := opts.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	permMode := opts.PermissionsMode
	if permMode == "" {
		permMode = "ask"
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &Client{
		apiKey:     apiKey,
		model:      model,
		proxyURL:   strings.TrimRight(proxyURL, "/"),
		workDir:    workDir,
		maxTurns:   maxTurns,
		timeout:    timeout,
		permMode:   permMode,
		maxCost:    opts.MaxCost,
		httpClient: httpClient,
		debug:      opts.Debug,
	}
}

// SessionID returns the current session ID.
func (c *Client) SessionID() string { return c.sessionID }

// TotalCost returns the accumulated cost in USD.
func (c *Client) TotalCost() float64 { return c.totalCost }

// TotalTokens returns the accumulated token usage.
func (c *Client) TotalTokens() TokenUsage { return c.totalTokens }

// Run sends a prompt and blocks until the agent finishes.
func (c *Client) Run(ctx context.Context, prompt string) (*Result, error) {
	var result Result
	var lastText strings.Builder

	for event := range c.Stream(ctx, prompt) {
		if event.Err != nil {
			return nil, event.Err
		}

		switch event.Type {
		case EventText:
			lastText.WriteString(event.Content)
		case EventComplete:
			result = Result{
				Success:      true,
				Status:       "completed",
				Summary:      event.Summary,
				FilesChanged: event.FilesChanged,
				Tokens:       event.Tokens,
				Cost:         event.Cost,
				DurationMs:   event.DurationMs,
				SessionID:    c.sessionID,
				Model:        c.model,
			}
		case EventError:
			result = Result{
				Success:   false,
				Status:    "failed",
				Summary:   event.Content,
				Tokens:    c.totalTokens,
				Cost:      c.totalCost,
				SessionID: c.sessionID,
				Model:     c.model,
				Error:     event.Content,
			}
		}
	}

	if result.Summary == "" && lastText.Len() > 0 {
		result.Summary = lastText.String()
	}

	return &result, nil
}

// Stream sends a prompt and returns a channel of events.
func (c *Client) Stream(ctx context.Context, prompt string) <-chan Event {
	ch := make(chan Event, 64)

	go func() {
		defer close(ch)

		body := map[string]any{
			"prompt":    prompt,
			"model":     c.model,
			"workspace": c.workDir,
			"sessionId": c.sessionID,
			"maxTurns":  c.maxTurns,
			"permissions": map[string]any{
				"mode": c.permMode,
			},
		}

		if schemas := c.toolSchemas(); len(schemas) > 0 {
			body["tools"] = schemas
		}

		if c.maxCost > 0 {
			body["permissions"].(map[string]any)["maxCost"] = c.maxCost
		}

		jsonBody, err := json.Marshal(body)
		if err != nil {
			ch <- Event{Type: EventError, Err: fmt.Errorf("marshal body: %w", err)}
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", c.proxyURL+"/v1/agent/run", bytes.NewReader(jsonBody))
		if err != nil {
			ch <- Event{Type: EventError, Err: fmt.Errorf("create request: %w", err)}
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "text/event-stream")

		start := time.Now()

		resp, err := c.httpClient.Do(req)
		if err != nil {
			ch <- Event{Type: EventError, Err: fmt.Errorf("http request: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			err := fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
			if resp.StatusCode == http.StatusNotFound {
				// The hosted run endpoint is not deployed on the default
				// ProxyURL. Say what to use instead of a bare 404.
				err = fmt.Errorf("%w: %s has no hosted agent endpoint (/v1/agent/run); run the agent locally with Query/Open (needs `mc` >= %s), see https://github.com/MonkeysCloud/monkeyscode-go#local-agent", ErrHostedRunUnavailable, c.proxyURL, MinCLIVersion)
			}
			ch <- Event{
				Type:    EventError,
				Content: string(bodyBytes),
				Err:     err,
			}
			return
		}

		// Parse SSE stream
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimSpace(line[6:])
			if data == "[DONE]" {
				break
			}

			var raw map[string]any
			if err := json.Unmarshal([]byte(data), &raw); err != nil {
				continue
			}

			event := c.mapSSEEvent(raw, start)
			if event == nil {
				continue
			}

			// Track session ID
			if sid, ok := raw["sessionId"].(string); ok && sid != "" {
				c.sessionID = sid
			}

			// Track tokens and cost
			if event.Type == EventComplete {
				c.totalTokens = event.Tokens
				c.totalCost += event.Cost
			}

			ch <- *event
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			ch <- Event{Type: EventError, Err: fmt.Errorf("read stream: %w", err)}
		}
	}()

	return ch
}

// mapSSEEvent maps a raw SSE JSON object to an Event.
func (c *Client) mapSSEEvent(raw map[string]any, start time.Time) *Event {
	eventType, _ := raw["type"].(string)
	now := time.Now().UnixMilli()

	switch eventType {
	case "text":
		return &Event{Type: EventText, Timestamp: now, Content: strVal(raw, "content")}
	case "thinking":
		return &Event{Type: EventThinking, Timestamp: now, Content: strVal(raw, "content")}
	case "tool_call":
		return &Event{Type: EventToolCall, Timestamp: now, Tool: strVal(raw, "tool"), Content: strVal(raw, "args")}
	case "tool_result":
		return &Event{Type: EventToolResult, Timestamp: now, Tool: strVal(raw, "tool"), Content: strVal(raw, "result")}
	case "file_edit":
		return &Event{Type: EventFileEdit, Timestamp: now, Content: strVal(raw, "path")}
	case "error":
		return &Event{Type: EventError, Timestamp: now, Content: strVal(raw, "message")}
	case "complete":
		tokens := TokenUsage{}
		if t, ok := raw["tokens"].(map[string]any); ok {
			tokens.Input = intVal(t, "input")
			tokens.Output = intVal(t, "output")
			tokens.Thinking = intVal(t, "thinking")
			tokens.Total = intVal(t, "total")
		}
		cost, _ := raw["cost"].(float64)
		return &Event{
			Type:       EventComplete,
			Timestamp:  now,
			Summary:    strVal(raw, "summary"),
			Tokens:     tokens,
			Cost:       cost,
			DurationMs: time.Since(start).Milliseconds(),
		}
	default:
		return nil
	}
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func intVal(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}
