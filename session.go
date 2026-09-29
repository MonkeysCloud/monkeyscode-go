package monkeyscode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Session represents a resumable agent conversation.
type Session struct {
	// ID is the unique session identifier.
	ID string

	// CreatedAt is when the session was created.
	CreatedAt time.Time

	// UpdatedAt is when the session was last used.
	UpdatedAt time.Time

	// Model used in this session.
	Model string

	// TotalCost accumulated across all runs in this session.
	TotalCost float64

	// TotalTokens accumulated across all runs.
	TotalTokens TokenUsage

	// RunCount is the number of runs in this session.
	RunCount int
}

// SessionOptions configures session behavior.
type SessionOptions struct {
	// SessionID to resume. If empty, a new session is created.
	SessionID string

	// ForkFrom creates a new session branching from this session ID.
	ForkFrom string
}

// SessionManager manages agent sessions for the client.
type SessionManager struct {
	client *Client
}

// NewSessionManager creates a session manager for the given client.
func NewSessionManager(client *Client) *SessionManager {
	return &SessionManager{client: client}
}

// Resume resumes an existing session by ID.
// The next Run() or Stream() call will continue in this session.
func (sm *SessionManager) Resume(sessionID string) {
	sm.client.sessionID = sessionID
}

// Fork creates a new session that branches from an existing one.
// Returns the new session ID.
func (sm *SessionManager) Fork(ctx context.Context, fromSessionID string) (string, error) {
	url := fmt.Sprintf("%s/v1/sessions/%s/fork", sm.client.proxyURL, fromSessionID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("create fork request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+sm.client.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := sm.client.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fork session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("fork session: HTTP %d", resp.StatusCode)
	}

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode fork response: %w", err)
	}

	sm.client.sessionID = result.SessionID
	return result.SessionID, nil
}

// Get retrieves session details by ID.
func (sm *SessionManager) Get(ctx context.Context, sessionID string) (*Session, error) {
	url := fmt.Sprintf("%s/v1/sessions/%s", sm.client.proxyURL, sessionID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+sm.client.apiKey)

	resp, err := sm.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get session: HTTP %d", resp.StatusCode)
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	return &session, nil
}

// List returns recent sessions for the authenticated user.
func (sm *SessionManager) List(ctx context.Context, limit int) ([]Session, error) {
	url := fmt.Sprintf("%s/v1/sessions?limit=%d", sm.client.proxyURL, limit)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+sm.client.apiKey)

	resp, err := sm.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list sessions: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Sessions []Session `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}
	return result.Sessions, nil
}

// Current returns the current session ID, or empty if none.
func (sm *SessionManager) Current() string {
	return sm.client.sessionID
}

// Clear resets the session ID, starting a new conversation.
func (sm *SessionManager) Clear() {
	sm.client.sessionID = ""
}

// RunInSession runs a prompt in a specific session.
func (sm *SessionManager) RunInSession(ctx context.Context, sessionID, prompt string) (*Result, error) {
	oldSession := sm.client.sessionID
	sm.client.sessionID = sessionID
	result, err := sm.client.Run(ctx, prompt)
	if err != nil {
		sm.client.sessionID = oldSession
		return nil, err
	}
	return result, nil
}

// CompactSession formats a session summary string.
func CompactSession(s *Session) string {
	return fmt.Sprintf("[%s] %s — %d runs, $%.4f",
		s.ID,
		s.Model,
		s.RunCount,
		s.TotalCost,
	)
}

// Sessions returns a SessionManager for this client.
func (c *Client) Sessions() *SessionManager {
	return NewSessionManager(c)
}

// AddTool registers a custom tool with this client.
func (c *Client) AddTool(tool *ToolDefinition) {
	if c.tools == nil {
		c.tools = NewToolRegistry()
	}
	c.tools.Add(tool)
}

// RemoveTool unregisters a custom tool.
func (c *Client) RemoveTool(name string) {
	if c.tools != nil {
		c.tools.Remove(name)
	}
}

// ListTools returns the names of all registered tools.
func (c *Client) ListTools() []string {
	if c.tools == nil {
		return nil
	}
	return c.tools.List()
}

// toolSchemas returns JSON schemas for all custom tools (used in request body).
func (c *Client) toolSchemas() []map[string]any {
	if c.tools == nil || c.tools.Count() == 0 {
		return nil
	}
	return c.tools.ToJSONSchemas()
}

// hasCustomTool returns true if the named tool is a custom tool.
func (c *Client) hasCustomTool(name string) bool {
	return c.tools != nil && c.tools.Has(name)
}

// executeCustomTool runs a custom tool and returns the result.
func (c *Client) executeCustomTool(ctx context.Context, name, args string) ToolResult {
	if c.tools == nil {
		return ToolResult{Tool: name, Result: "no tools registered", Success: false}
	}
	return c.tools.Execute(ctx, name, args)
}

// formatSessionID returns a short session ID for display.
func formatSessionID(id string) string {
	if len(id) > 12 {
		return id[:12] + "..."
	}
	return id
}

// isResumeSession returns true if the session ID looks like a valid session.
func isResumeSession(id string) bool {
	return id != "" && !strings.HasPrefix(id, "pending")
}
