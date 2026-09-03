package monkeyscode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Agent represents an agent bound to a workspace.
type Agent struct {
	client    *Client
	workspace string
	sessionID string
}

// Run executes a prompt synchronously and returns the final result.
func (a *Agent) Run(ctx context.Context, prompt string) (*RunResult, error) {
	ch := a.Stream(ctx, prompt)

	var result *RunResult
	for ev := range ch {
		if ev.Type == EventComplete {
			result = &RunResult{
				Success:      true,
				Summary:      ev.Summary,
				FilesChanged: ev.Files,
				Cost:         ev.Cost,
				DurationMs:   ev.DurationMs,
				SessionID:    a.sessionID,
				Model:        a.client.config.Model,
			}
			if ev.Tokens != nil {
				result.Tokens = *ev.Tokens
			}
		}
		if ev.Type == EventError {
			return &RunResult{
				Success:  false,
				Summary:  ev.Message,
				ExitCode: 1,
				Model:    a.client.config.Model,
			}, fmt.Errorf("agent error [%s]: %s", ev.Code, ev.Message)
		}
	}

	if result == nil {
		return &RunResult{
			Success:  false,
			Summary:  "Agent failed with no completion event",
			ExitCode: 1,
			Model:    a.client.config.Model,
		}, fmt.Errorf("no completion event received")
	}

	return result, nil
}

// Stream sends a prompt and returns a channel of events.
// The channel is closed when the run completes or an error occurs.
func (a *Agent) Stream(ctx context.Context, prompt string) <-chan *Event {
	ch := make(chan *Event, 64)

	go func() {
		defer close(ch)
		a.streamSSE(ctx, prompt, ch)
	}()

	return ch
}

// Goal runs the agent in goal mode — iterates until the goal is met.
func (a *Agent) Goal(ctx context.Context, prompt string, opts GoalOptions) (*GoalResult, error) {
	if opts.MaxIterations == 0 {
		opts.MaxIterations = 10
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}

	startTime := time.Now()
	var summaries []string
	var lastResult *RunResult
	goalMet := false

	goalPrompt := prompt
	if opts.VerifyCommand != "" {
		goalPrompt = fmt.Sprintf(
			"%s\n\nVerification: Run `%s` after each attempt. Keep iterating until it passes.",
			prompt, opts.VerifyCommand,
		)
	}

	for i := 1; i <= opts.MaxIterations; i++ {
		if time.Since(startTime) > opts.Timeout {
			break
		}
		if opts.MaxCost > 0 && lastResult != nil && lastResult.Cost >= opts.MaxCost {
			break
		}

		iterPrompt := goalPrompt
		if i > 1 && lastResult != nil {
			iterPrompt = fmt.Sprintf("Continue working on the goal. Previous: %s", lastResult.Summary)
		}

		result, err := a.Run(ctx, iterPrompt)
		if err != nil {
			lastResult = result
			summaries = append(summaries, result.Summary)
			continue
		}

		lastResult = result
		summaries = append(summaries, result.Summary)

		if result.Success && result.ExitCode == 0 {
			goalMet = true
			break
		}
	}

	gr := &GoalResult{
		GoalMet:            goalMet,
		Iterations:         len(summaries),
		IterationSummaries: summaries,
		TotalDuration:      time.Since(startTime),
	}
	if lastResult != nil {
		gr.RunResult = *lastResult
	}
	gr.Success = goalMet
	if goalMet {
		gr.ExitCode = 0
	} else {
		gr.ExitCode = 1
	}

	return gr, nil
}

// GoalOptions configures goal-mode execution.
type GoalOptions struct {
	VerifyCommand string
	MaxIterations int
	Timeout       time.Duration
	MaxCost       float64
}

// SessionID returns the current session ID, if assigned by the server.
func (a *Agent) SessionID() string {
	return a.sessionID
}

// ── SSE streaming ───────────────────────────────────────────────────

func (a *Agent) streamSSE(ctx context.Context, prompt string, ch chan<- *Event) {
	cfg := a.client.config

	body := map[string]any{
		"prompt":    prompt,
		"model":     cfg.Model,
		"workspace": a.workspace,
		"sessionId": a.sessionID,
		"maxTurns":  cfg.MaxTurns,
		"permissions": map[string]any{
			"mode": cfg.PermissionsMode,
		},
	}

	if cfg.MaxCost > 0 {
		body["permissions"].(map[string]any)["maxCost"] = cfg.MaxCost
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		ch <- errorEvent("MARSHAL_ERROR", err.Error())
		return
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.ProxyURL+"/v1/agent/run",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		ch <- errorEvent("REQUEST_ERROR", err.Error())
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := a.client.httpClient.Do(req)
	if err != nil {
		ch <- errorEvent("CONNECTION_ERROR", err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		ch <- errorEvent(
			fmt.Sprintf("HTTP_%d", resp.StatusCode),
			string(bodyBytes),
		)
		return
	}

	// Parse SSE stream
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20) // 1MB buffer

	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line = end of event
		if line == "" {
			if dataBuf.Len() > 0 {
				var ev Event
				if err := json.Unmarshal([]byte(dataBuf.String()), &ev); err == nil {
					// Track session
					if ev.SessionID != "" {
						a.sessionID = ev.SessionID
					}
					ch <- &ev
				}
				dataBuf.Reset()
			}
			continue
		}

		// SSE field parsing
		if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			dataBuf.WriteString(line[5:])
		}
		// Ignore "event:", "id:", "retry:" fields
	}

	if err := scanner.Err(); err != nil {
		ch <- errorEvent("STREAM_ERROR", err.Error())
	}
	// Flush remaining data
	if dataBuf.Len() > 0 {
		var ev Event
		if err := json.Unmarshal([]byte(dataBuf.String()), &ev); err == nil {
			if ev.SessionID != "" {
				a.sessionID = ev.SessionID
			}
			ch <- &ev
		}
	}
}

func errorEvent(code, message string) *Event {
	return &Event{
		Type:      EventError,
		Timestamp: time.Now().UnixMilli(),
		Code:      code,
		Message:   message,
	}
}
