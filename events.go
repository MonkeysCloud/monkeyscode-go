package monkeyscode

import "time"

// EventType defines the type of an agent event.
type EventType string

const (
	EventStart           EventType = "start"
	EventText            EventType = "text"
	EventThinking        EventType = "thinking"
	EventToolCall        EventType = "tool_call"
	EventToolResult      EventType = "tool_result"
	EventFileEdit        EventType = "file_edit"
	EventFileRead        EventType = "file_read"
	EventTerminalCommand EventType = "terminal_command"
	EventSearchResult    EventType = "search_result"
	EventPlanReady       EventType = "plan_ready"
	EventCostUpdate      EventType = "cost_update"
	EventAgentSpawned    EventType = "agent_spawned"
	EventAgentComplete   EventType = "agent_complete"
	EventError           EventType = "error"
	EventComplete        EventType = "complete"
)

// Event represents an event emitted by the agent loop.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp int64     `json:"timestamp,omitempty"`

	// Text / Thinking
	Content string `json:"content,omitempty"`

	// Tool calls
	Tool    string         `json:"tool,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Result  string         `json:"result,omitempty"`
	Summary string         `json:"summary,omitempty"`
	Success *bool          `json:"success,omitempty"`

	// File operations
	Path         string `json:"path,omitempty"`
	Diff         string `json:"diff,omitempty"`
	LinesAdded   int    `json:"linesAdded,omitempty"`
	LinesRemoved int    `json:"linesRemoved,omitempty"`

	// Terminal
	Command  string `json:"command,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
	Output   string `json:"output,omitempty"`

	// Cost tracking
	TokensIn       int     `json:"tokens_in,omitempty"`
	TokensOut      int     `json:"tokens_out,omitempty"`
	TokensThinking int     `json:"tokens_thinking,omitempty"`
	Cost           float64 `json:"cost,omitempty"`

	// Subagents
	AgentID string `json:"agentId,omitempty"`
	Task    string `json:"task,omitempty"`

	// Error
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`

	// Complete
	Tokens     *TokenUsage  `json:"tokens,omitempty"`
	DurationMs int          `json:"durationMs,omitempty"`
	Files      []FileChange `json:"filesChanged,omitempty"`

	// Internal: session tracking
	SessionID string `json:"sessionId,omitempty"`
}

// TokenUsage tracks token consumption for a run.
type TokenUsage struct {
	Input    int `json:"input"`
	Output   int `json:"output"`
	Thinking int `json:"thinking"`
	Total    int `json:"total"`
}

// FileChange records a file modification made by the agent.
type FileChange struct {
	Path         string `json:"path"`
	Action       string `json:"action"`
	Diff         string `json:"diff,omitempty"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
}

// RunResult is the final result of a completed run.
type RunResult struct {
	Success      bool         `json:"success"`
	Summary      string       `json:"summary"`
	FilesChanged []FileChange `json:"filesChanged"`
	Tokens       TokenUsage   `json:"tokens"`
	Cost         float64      `json:"cost"`
	DurationMs   int          `json:"durationMs"`
	SessionID    string       `json:"sessionId"`
	ExitCode     int          `json:"exitCode"`
	Model        string       `json:"model"`
}

// GoalResult is the result of a goal-mode run.
type GoalResult struct {
	RunResult
	GoalMet            bool          `json:"goalMet"`
	Iterations         int           `json:"iterations"`
	IterationSummaries []string      `json:"iterationSummaries"`
	TotalDuration      time.Duration `json:"-"`
}
