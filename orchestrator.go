package monkeyscode

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// OrchestratorResult is the combined result of a parallel or fan-out run.
type OrchestratorResult struct {
	// Results from each agent slot.
	Results []AgentSlotResult

	// TotalCost across all agents.
	TotalCost float64

	// TotalTokens across all agents.
	TotalTokens TokenUsage

	// DurationMs for the entire orchestration.
	DurationMs int64

	// SuccessCount is the number of successful slots.
	SuccessCount int

	// FailCount is the number of failed slots.
	FailCount int
}

// AgentSlotResult is the result from one agent slot in a parallel run.
type AgentSlotResult struct {
	// Index of this slot in the input array.
	Index int

	// Prompt that was sent to this agent.
	Prompt string

	// Result from the agent.
	Result *Result

	// Error if the agent failed.
	Error error

	// DurationMs for this slot.
	DurationMs int64
}

// ParallelOptions configures RunParallel.
type ParallelOptions struct {
	// Prompts to run in parallel.
	Prompts []string

	// Concurrency is the max simultaneous agents (default: len(Prompts), max: 10).
	Concurrency int
}

// FanOutOptions configures RunFanOut.
type FanOutOptions struct {
	// Prompt is the base instruction.
	Prompt string

	// Targets are the items to fan out over (file paths, modules, etc.).
	Targets []string

	// Concurrency is the max simultaneous agents (default: len(Targets), max: 10).
	Concurrency int
}

// RunParallel runs multiple prompts concurrently using separate agent instances.
//
//	result, err := RunParallel(ctx, client, ParallelOptions{
//	    Prompts:     []string{"Fix auth.go", "Fix db.go", "Fix api.go"},
//	    Concurrency: 3,
//	})
func RunParallel(ctx context.Context, client *Client, opts ParallelOptions) (*OrchestratorResult, error) {
	if len(opts.Prompts) == 0 {
		return nil, fmt.Errorf("RunParallel: at least one prompt is required")
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = len(opts.Prompts)
	}
	if concurrency > 10 {
		concurrency = 10
	}

	start := time.Now()
	results := make([]AgentSlotResult, len(opts.Prompts))
	var totalCost atomic.Int64 // stored as microdollars (cost * 1e6)
	var totalInput atomic.Int64
	var totalOutput atomic.Int64
	var successCount atomic.Int32
	var failCount atomic.Int32

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, prompt := range opts.Prompts {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			slotStart := time.Now()
			child := NewClient(Options{
				APIKey:           client.apiKey,
				Model:            client.model,
				ProxyURL:         client.proxyURL,
				WorkingDirectory: client.workDir,
				MaxTurns:         client.maxTurns,
				Timeout:          client.timeout,
				PermissionsMode:  client.permMode,
				Debug:            client.debug,
			})

			result, err := child.Run(ctx, p)
			elapsed := time.Since(slotStart).Milliseconds()

			results[idx] = AgentSlotResult{
				Index:      idx,
				Prompt:     p,
				Result:     result,
				Error:      err,
				DurationMs: elapsed,
			}

			if err == nil && result != nil {
				totalCost.Add(int64(result.Cost * 1e6))
				totalInput.Add(result.Tokens.Input)
				totalOutput.Add(result.Tokens.Output)
				if result.Success {
					successCount.Add(1)
				} else {
					failCount.Add(1)
				}
			} else {
				failCount.Add(1)
			}
		}(i, prompt)
	}

	wg.Wait()

	return &OrchestratorResult{
		Results:      results,
		TotalCost:    float64(totalCost.Load()) / 1e6,
		TotalTokens:  TokenUsage{Input: totalInput.Load(), Output: totalOutput.Load(), Total: totalInput.Load() + totalOutput.Load()},
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: int(successCount.Load()),
		FailCount:    int(failCount.Load()),
	}, nil
}

// RunFanOut runs the same prompt against multiple targets concurrently.
//
//	result, err := RunFanOut(ctx, client, FanOutOptions{
//	    Prompt:      "Review this file for security issues",
//	    Targets:     []string{"auth.go", "db.go", "api.go"},
//	    Concurrency: 3,
//	})
func RunFanOut(ctx context.Context, client *Client, opts FanOutOptions) (*OrchestratorResult, error) {
	if len(opts.Targets) == 0 {
		return nil, fmt.Errorf("RunFanOut: at least one target is required")
	}

	prompts := make([]string, len(opts.Targets))
	for i, target := range opts.Targets {
		prompts[i] = fmt.Sprintf("%s\n\nTarget: %s", opts.Prompt, target)
	}

	return RunParallel(ctx, client, ParallelOptions{
		Prompts:     prompts,
		Concurrency: opts.Concurrency,
	})
}

// Semaphore is a counting semaphore for controlling concurrency.
type Semaphore struct {
	ch chan struct{}
}

// NewSemaphore creates a semaphore with the given capacity.
func NewSemaphore(n int) *Semaphore {
	return &Semaphore{ch: make(chan struct{}, n)}
}

// Acquire blocks until a slot is available.
func (s *Semaphore) Acquire() { s.ch <- struct{}{} }

// Release frees a slot.
func (s *Semaphore) Release() { <-s.ch }

// TryAcquire attempts to acquire without blocking. Returns true if successful.
func (s *Semaphore) TryAcquire() bool {
	select {
	case s.ch <- struct{}{}:
		return true
	default:
		return false
	}
}
