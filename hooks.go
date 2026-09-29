package monkeyscode

import (
	"fmt"
	"log"
	"sync"
)

// HookAction describes what to do with a tool call after pre-hooks run.
type HookAction string

const (
	HookAllow  HookAction = "allow"
	HookBlock  HookAction = "block"
	HookModify HookAction = "modify"
)

// PreToolContext is passed to pre-tool hooks.
type PreToolContext struct {
	Tool      string
	Args      map[string]any
	RunID     string
	TurnIndex int
	SessionID string
	Timestamp int64
}

// PreToolResult is returned by pre-tool hooks.
type PreToolResult struct {
	Action HookAction
	Args   map[string]any // for Modify
	Reason string         // for Block
}

// PostToolContext is passed to post-tool hooks.
type PostToolContext struct {
	Tool       string
	Args       map[string]any
	Result     any
	IsError    bool
	DurationMs int64
	RunID      string
	TurnIndex  int
	SessionID  string
	Timestamp  int64
}

// PreToolHook is a function that intercepts tool calls before execution.
type PreToolHook func(ctx PreToolContext) PreToolResult

// PostToolHook is a function that observes tool results after execution.
type PostToolHook func(ctx PostToolContext)

// HookManager manages pre- and post-tool hooks.
//
// Hook execution order:
//   - All pre-hooks run in registration order
//   - First hook to return Block wins (short-circuits)
//   - Modify results accumulate (last args win)
//   - Post-hooks are fire-and-forget (errors logged, never raised)
type HookManager struct {
	mu        sync.RWMutex
	preHooks  []PreToolHook
	postHooks []PostToolHook
}

// NewHookManager creates an empty hook manager.
func NewHookManager() *HookManager {
	return &HookManager{}
}

// HasHooks returns true if any hooks are registered.
func (hm *HookManager) HasHooks() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return len(hm.preHooks) > 0 || len(hm.postHooks) > 0
}

// PreHookCount returns the number of registered pre-hooks.
func (hm *HookManager) PreHookCount() int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return len(hm.preHooks)
}

// PostHookCount returns the number of registered post-hooks.
func (hm *HookManager) PostHookCount() int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return len(hm.postHooks)
}

// AddPreHook registers a pre-tool hook.
func (hm *HookManager) AddPreHook(hook PreToolHook) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.preHooks = append(hm.preHooks, hook)
}

// AddPostHook registers a post-tool hook.
func (hm *HookManager) AddPostHook(hook PostToolHook) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.postHooks = append(hm.postHooks, hook)
}

// ClearAll removes all hooks.
func (hm *HookManager) ClearAll() {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.preHooks = nil
	hm.postHooks = nil
}

// RunPreHooks executes all pre-hooks in order.
//
//   - First 'block' result short-circuits and is returned.
//   - 'modify' results accumulate (args are merged).
//   - If all return 'allow', returns PreToolResult{Action: HookAllow}.
//   - Panics in hooks are recovered and logged.
func (hm *HookManager) RunPreHooks(ctx PreToolContext) PreToolResult {
	hm.mu.RLock()
	hooks := make([]PreToolHook, len(hm.preHooks))
	copy(hooks, hm.preHooks)
	hm.mu.RUnlock()

	if len(hooks) == 0 {
		return PreToolResult{Action: HookAllow}
	}

	currentArgs := copyMap(ctx.Args)
	var lastModify *PreToolResult

	for _, hook := range hooks {
		result := safeRunPreHook(hook, PreToolContext{
			Tool:      ctx.Tool,
			Args:      currentArgs,
			RunID:     ctx.RunID,
			TurnIndex: ctx.TurnIndex,
			SessionID: ctx.SessionID,
			Timestamp: ctx.Timestamp,
		})

		switch result.Action {
		case HookBlock:
			return result
		case HookModify:
			if result.Args != nil {
				for k, v := range result.Args {
					currentArgs[k] = v
				}
				merged := copyMap(currentArgs)
				lastModify = &PreToolResult{Action: HookModify, Args: merged}
			}
		}
	}

	if lastModify != nil {
		return *lastModify
	}
	return PreToolResult{Action: HookAllow}
}

// RunPostHooks executes all post-hooks. Errors are logged, never returned.
func (hm *HookManager) RunPostHooks(ctx PostToolContext) {
	hm.mu.RLock()
	hooks := make([]PostToolHook, len(hm.postHooks))
	copy(hooks, hm.postHooks)
	hm.mu.RUnlock()

	for _, hook := range hooks {
		safeRunPostHook(hook, ctx)
	}
}

// safeRunPreHook runs a pre-hook with panic recovery.
func safeRunPreHook(hook PreToolHook, ctx PreToolContext) (result PreToolResult) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("monkeyscode: pre-hook panic (tool: %s): %v", ctx.Tool, r)
			result = PreToolResult{Action: HookAllow}
		}
	}()
	return hook(ctx)
}

// safeRunPostHook runs a post-hook with panic recovery.
func safeRunPostHook(hook PostToolHook, ctx PostToolContext) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("monkeyscode: post-hook panic (tool: %s): %v", ctx.Tool, r)
		}
	}()
	hook(ctx)
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return make(map[string]any)
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// OnPreToolUse registers a pre-tool hook on the client. Returns the client for chaining.
func (c *Client) OnPreToolUse(hook PreToolHook) *Client {
	if c.hooks == nil {
		c.hooks = NewHookManager()
	}
	c.hooks.AddPreHook(hook)
	return c
}

// OnPostToolUse registers a post-tool hook on the client. Returns the client for chaining.
func (c *Client) OnPostToolUse(hook PostToolHook) *Client {
	if c.hooks == nil {
		c.hooks = NewHookManager()
	}
	c.hooks.AddPostHook(hook)
	return c
}

// ClearHooks removes all hooks from the client.
func (c *Client) ClearHooks() {
	if c.hooks != nil {
		c.hooks.ClearAll()
	}
}

// hookManagerHasHooks returns true if the client has any hooks.
func (c *Client) hookManagerHasHooks() bool {
	return c.hooks != nil && c.hooks.HasHooks()
}

// runPreHooks runs the client's pre-hooks. Returns HookAllow if no hooks.
func (c *Client) runPreHooks(ctx PreToolContext) PreToolResult {
	if c.hooks == nil {
		return PreToolResult{Action: HookAllow}
	}
	return c.hooks.RunPreHooks(ctx)
}

// runPostHooks runs the client's post-hooks.
func (c *Client) runPostHooks(ctx PostToolContext) {
	if c.hooks != nil {
		c.hooks.RunPostHooks(ctx)
	}
}

// blockMessage formats a blocked tool message.
func blockMessage(reason string) string {
	if reason == "" {
		return "Blocked by hook: no reason given"
	}
	return fmt.Sprintf("Blocked by hook: %s", reason)
}
