package monkeyscode

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxSubagents is the maximum concurrent subagents per parent.
	MaxSubagents = 4

	// MaxSubagentDepth is the maximum nesting depth.
	MaxSubagentDepth = 2

	// SummaryMaxChars is the default summary cap.
	SummaryMaxChars = 3000

	// DefaultSubagentMaxTurns is the default max turns for subagents.
	DefaultSubagentMaxTurns = 20

	// DefaultSubagentTimeout is the default timeout for subagents.
	DefaultSubagentTimeout = 2 * time.Minute
)

// SubagentPreset is a predefined behavior for subagents.
type SubagentPreset string

const (
	PresetExplorer SubagentPreset = "explorer"
	PresetReviewer SubagentPreset = "reviewer"
)

var presetPrompts = map[SubagentPreset]string{
	PresetExplorer: "You are a read-only codebase explorer. Answer the task by reading files " +
		"and searching the workspace. You cannot modify anything. Be thorough but conclude " +
		"quickly: when you have the answer, respond with a concise, self-contained summary " +
		"(file paths, line numbers, key findings). Your final message is the ONLY thing your " +
		"caller sees — include everything relevant in it.",
	PresetReviewer: "You are a read-only code reviewer. Use git status, diff, log, blame, and " +
		"file reads to critique the current changes: correctness risks, missing tests, style " +
		"inconsistencies, security concerns. You cannot modify anything. Respond with a concise " +
		"review — your final message is the ONLY thing your caller sees.",
}

// SubagentOptions configures a subagent spawn.
type SubagentOptions struct {
	// Task is the instruction for the subagent.
	Task string

	// Preset is the behavior preset: "explorer" or "reviewer".
	Preset SubagentPreset

	// Context is additional context from the parent agent.
	Context string

	// File to focus on (optional).
	File string

	// MaxTurns for the subagent (default: 20).
	MaxTurns int

	// Timeout for the subagent (default: 2m).
	Timeout time.Duration
}

// SubagentResult is the output of a subagent run.
type SubagentResult struct {
	// ID is the subagent identifier.
	ID string

	// Success indicates whether the subagent completed successfully.
	Success bool

	// Summary is the capped output from the subagent.
	Summary string

	// Result is the full agent result.
	Result *Result

	// DurationMs is how long the subagent ran.
	DurationMs int64
}

// Spawn creates a focused child agent for a specific task.
//
// Subagents run with autoApprove permissions and capped summary.
// Max 4 concurrent subagents per parent. Max depth: 2 levels.
func (c *Client) Spawn(ctx context.Context, opts SubagentOptions) (*SubagentResult, error) {
	return spawnSubagent(ctx, c, opts, 0)
}

func spawnSubagent(ctx context.Context, parent *Client, opts SubagentOptions, depth int) (*SubagentResult, error) {
	subID := fmt.Sprintf("sub_%d", time.Now().UnixMilli())

	if depth >= MaxSubagentDepth {
		return &SubagentResult{
			ID:      subID,
			Success: false,
			Summary: "Error: Subagent depth limit reached (max 2 levels).",
			Result: &Result{
				Success: false,
				Status:  "failed",
				Summary: "Depth limit reached",
				Error:   "Subagent depth limit reached",
			},
			DurationMs: 0,
		}, nil
	}

	preset := opts.Preset
	if _, ok := presetPrompts[preset]; !ok {
		preset = PresetExplorer
	}

	maxTurns := opts.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultSubagentMaxTurns
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSubagentTimeout
	}

	// Build prompt
	var parts []string
	if opts.Context != "" {
		parts = append(parts, "Context from the main agent:\n"+opts.Context)
	}
	if opts.File != "" {
		parts = append(parts, opts.Task+"\n\nFocus on this file: "+opts.File)
	} else {
		parts = append(parts, opts.Task)
	}
	prompt := presetPrompts[preset] + "\n\n" + strings.Join(parts, "\n\n")

	// Create child client
	child := NewClient(Options{
		APIKey:           parent.apiKey,
		Model:            parent.model,
		ProxyURL:         parent.proxyURL,
		WorkingDirectory: parent.workDir,
		MaxTurns:         maxTurns,
		Timeout:          timeout,
		PermissionsMode:  "autoApprove",
		Debug:            parent.debug,
	})

	start := time.Now()
	result, err := child.Run(ctx, prompt)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return &SubagentResult{
			ID:      subID,
			Success: false,
			Summary: fmt.Sprintf("Subagent error: %v", err),
			Result: &Result{
				Success:    false,
				Status:     "failed",
				Summary:    err.Error(),
				Error:      err.Error(),
				DurationMs: elapsed,
			},
			DurationMs: elapsed,
		}, nil
	}

	return &SubagentResult{
		ID:         subID,
		Success:    result.Success,
		Summary:    CapSummary(result.Summary, SummaryMaxChars),
		Result:     result,
		DurationMs: elapsed,
	}, nil
}

// CapSummary truncates a summary at a character limit, preferring sentence boundaries.
func CapSummary(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= maxChars {
		return trimmed
	}

	window := trimmed[:maxChars]
	minKeep := int(float64(maxChars) * 0.6)

	// Try paragraph boundary first
	para := strings.LastIndex(window, "\n\n")
	if para >= minKeep {
		return strings.TrimRight(trimmed[:para], " \n") + "\n[summary truncated]"
	}

	// Try sentence boundary
	sentence := strings.LastIndex(window, ". ")
	if sentence >= minKeep {
		return strings.TrimRight(trimmed[:sentence+1], " ") + "\n[summary truncated]"
	}

	sentenceNL := strings.LastIndex(window, ".\n")
	if sentenceNL >= minKeep {
		return strings.TrimRight(trimmed[:sentenceNL+1], " ") + "\n[summary truncated]"
	}

	return trimmed[:maxChars] + "\n[summary truncated]"
}
