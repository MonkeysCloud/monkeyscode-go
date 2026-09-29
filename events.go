package monkeyscode

// EventType identifies the kind of streaming event.
type EventType string

const (
	EventStart      EventType = "start"
	EventText       EventType = "text"
	EventThinking   EventType = "thinking"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventFileEdit   EventType = "file_edit"
	EventError      EventType = "error"
	EventComplete   EventType = "complete"
	EventCostUpdate EventType = "cost_update"
)

// Event represents a streaming event from the agent.
type Event struct {
	// Type of event.
	Type EventType

	// Timestamp in milliseconds since epoch.
	Timestamp int64

	// Content: text content, error message, or tool result.
	Content string

	// Tool name (for tool_call / tool_result events).
	Tool string

	// Summary (for complete events).
	Summary string

	// FilesChanged (for complete events).
	FilesChanged []FileChange

	// Tokens (for complete events).
	Tokens TokenUsage

	// Cost in USD (for complete events).
	Cost float64

	// DurationMs (for complete events).
	DurationMs int64

	// Err is set on error events.
	Err error
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	Input    int64
	Output   int64
	Thinking int64
	Total    int64
}

// FileChange represents a modified file.
type FileChange struct {
	Path   string
	Action string // "created", "modified", "deleted"
	Diff   string
}

// Result is the outcome of a completed agent run.
type Result struct {
	// Success indicates whether the run completed successfully.
	Success bool

	// Status: "completed", "failed", "timeout", "cancelled".
	Status string

	// Summary of what the agent did.
	Summary string

	// FilesChanged during the run.
	FilesChanged []FileChange

	// Tokens used.
	Tokens TokenUsage

	// Cost in USD.
	Cost float64

	// DurationMs for the entire run.
	DurationMs int64

	// SessionID for resuming.
	SessionID string

	// Model used.
	Model string

	// Error message if failed.
	Error string
}
