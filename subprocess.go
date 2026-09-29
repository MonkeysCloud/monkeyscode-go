package monkeyscode

// Local agent over the `mc` subprocess (stream-json, schema_version 1).
//
// The Client in monkeyscode.go talks to the cloud API; this file drives an
// installed CLI instead, so hooks, MCP servers, settings and permission
// rules all apply:
//
//	events, sess, err := monkeyscode.Query(ctx, "fix the failing test", monkeyscode.CLIOptions{
//	    CanUseTool: func(ctx context.Context, tool string, input map[string]any, pc monkeyscode.PermissionContext) monkeyscode.PermissionResult {
//	        if tool == "run_command" { return monkeyscode.Allow() }
//	        return monkeyscode.Deny("read-only please")
//	    },
//	})
//	if err != nil { log.Fatal(err) }
//	for ev := range events {
//	    if ev.Type == "result" { fmt.Println(ev.Result.Result) }
//	}
//	if err := sess.Wait(); err != nil { log.Fatal(err) }
//
// The TypeScript (@monkeyscode/sdk/subprocess) and Python
// (monkeyscode.cli_process) clients speak the same protocol.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// SupportedSchemaVersion is the stream-json schema this client understands.
const SupportedSchemaVersion = 1

// ── Events ──────────────────────────────────────────────────────────

// ContentBlock is a text or tool_use block of an assistant message, or a
// tool_result block of a user message.
type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// CLIMessage is the `message` of assistant and user events. For a prompt,
// Text holds the string content and Blocks is nil.
type CLIMessage struct {
	Role   string
	Text   string
	Blocks []ContentBlock
}

func (m *CLIMessage) UnmarshalJSON(b []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	if len(raw.Content) > 0 && raw.Content[0] == '"' {
		return json.Unmarshal(raw.Content, &m.Text)
	}
	if len(raw.Content) > 0 {
		return json.Unmarshal(raw.Content, &m.Blocks)
	}
	return nil
}

// PermissionDenial records a refused tool call.
type PermissionDenial struct {
	ToolName      string         `json:"tool_name"`
	ToolUseID     string         `json:"tool_use_id,omitempty"`
	ToolInput     map[string]any `json:"tool_input"`
	Reason        string         `json:"reason"`
	SuggestedRule string         `json:"suggested_rule,omitempty"`
}

// CLIUsage is the token usage of a turn.
type CLIUsage struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	CachedTokens   int `json:"cached_tokens"`
	ThinkingTokens int `json:"thinking_tokens"`
}

// CLIResult is the `result` event that ends every turn.
type CLIResult struct {
	Subtype           string             `json:"subtype"`
	IsError           bool               `json:"is_error"`
	Result            string             `json:"result"`
	Structured        json.RawMessage    `json:"structured,omitempty"`
	SessionID         string             `json:"session_id"`
	NumTurns          int                `json:"num_turns"`
	DurationMs        int64              `json:"duration_ms"`
	Usage             CLIUsage           `json:"usage"`
	CostUSD           float64            `json:"cost_usd"`
	IsEstimated       bool               `json:"is_estimated"`
	PermissionDenials []PermissionDenial `json:"permission_denials"`
	ExitCode          int                `json:"exit_code"`
	Error             string             `json:"error,omitempty"`
	SchemaErrors      []string           `json:"schema_errors,omitempty"`
}

// CLIInit is the `system/init` event.
type CLIInit struct {
	SchemaVersion  int                 `json:"schema_version"`
	SessionID      string              `json:"session_id"`
	Cwd            string              `json:"cwd"`
	Model          string              `json:"model"`
	PermissionMode string              `json:"permission_mode"`
	Tools          []string            `json:"tools"`
	McpServers     []map[string]string `json:"mcp_servers"`
	CLIVersion     string              `json:"cli_version"`
	ResumedFrom    string              `json:"resumed_from,omitempty"`
	ForkedFrom     string              `json:"forked_from,omitempty"`
}

// CLIEvent is one stream-json line. Type is "system", "assistant", "user",
// "result" or "error"; the matching field is set. Raw keeps the original line.
type CLIEvent struct {
	Type      string
	Subtype   string
	SessionID string
	Init      *CLIInit
	Message   *CLIMessage
	Result    *CLIResult
	Error     string
	Raw       json.RawMessage
}

// ParseCLIEvent decodes one stream-json line.
func ParseCLIEvent(line []byte) (CLIEvent, error) {
	var head struct {
		Type      string      `json:"type"`
		Subtype   string      `json:"subtype"`
		SessionID string      `json:"session_id"`
		Message   *CLIMessage `json:"message"`
		Error     string      `json:"error"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return CLIEvent{}, err
	}
	ev := CLIEvent{Type: head.Type, Subtype: head.Subtype, SessionID: head.SessionID, Message: head.Message, Error: head.Error, Raw: append(json.RawMessage(nil), line...)}
	switch {
	case head.Type == "system" && head.Subtype == "init":
		ev.Init = &CLIInit{}
		if err := json.Unmarshal(line, ev.Init); err != nil {
			return ev, err
		}
	case head.Type == "result":
		ev.Result = &CLIResult{}
		if err := json.Unmarshal(line, ev.Result); err != nil {
			return ev, err
		}
		ev.Subtype = ev.Result.Subtype
	}
	return ev, nil
}

// Text concatenates an assistant event's text blocks.
func (e CLIEvent) Text() string {
	if e.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range e.Message.Blocks {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// ── Permissions ─────────────────────────────────────────────────────

// PermissionContext describes an "ask" verdict sent over the control channel.
type PermissionContext struct {
	Description string
	Suggestions []string
	ToolUseID   string
}

// PermissionResult is the answer to a can_use_tool request.
type PermissionResult struct {
	Behavior     string         // "allow" or "deny"
	UpdatedInput map[string]any // allow only: run with these arguments instead
	Message      string         // deny only: fed back to the model
}

// Allow approves the call as is.
func Allow() PermissionResult { return PermissionResult{Behavior: "allow"} }

// AllowWith approves the call with rewritten input (re-checked against deny rules by the CLI).
func AllowWith(input map[string]any) PermissionResult {
	return PermissionResult{Behavior: "allow", UpdatedInput: input}
}

// Deny refuses the call; message is shown to the model.
func Deny(message string) PermissionResult {
	return PermissionResult{Behavior: "deny", Message: message}
}

// CanUseToolFunc decides "ask" verdicts. Deny rules, plan mode and hard
// refusals are decided by the CLI before it is called.
type CanUseToolFunc func(ctx context.Context, tool string, input map[string]any, pc PermissionContext) PermissionResult

// ── Options ─────────────────────────────────────────────────────────

// CLIOptions configures the `mc` subprocess. Zero values mean "CLI default".
type CLIOptions struct {
	// PathToMc is the executable, or an argv for a wrapper ([]string{"node", "cli.cjs"}).
	// Default: $MC_PATH, else "mc" on PATH.
	PathToMc []string

	Cwd             string
	Model           string
	PermissionMode  string // default | acceptEdits | plan | bypass
	Effort          string // low | medium | high
	AllowedTools    []string
	DisallowedTools []string
	AddDirs         []string
	// AutoApprove allows every ordinary ask when CanUseTool is nil.
	AutoApprove          bool
	CanUseTool           CanUseToolFunc
	PermissionTimeoutSec float64
	McpConfig            []string
	StrictMcpConfig      bool
	// IgnoreRepoSettings skips the repository's .monkeyscode settings, hooks and MCP servers.
	IgnoreRepoSettings bool
	// Settings is an extra settings layer: marshalled to inline JSON.
	Settings           map[string]any
	AppendSystemPrompt string
	SystemPromptFile   string
	MaxCostUSD         float64
	MaxTokens          int
	// JSONSchemaFile is a path to the schema for the final answer.
	JSONSchemaFile string
	Resume         string
	Continue       bool
	ForkSession    bool
	NoHooks        bool
	// TimeoutMs is a wall-clock limit; 0 = none.
	TimeoutMs int
	// Env is merged over os.Environ(); an empty value unsets the key.
	Env       map[string]string
	ExtraArgs []string
	// Stderr receives the CLI's diagnostic lines.
	Stderr func(line string)
}

// BuildCLIArgs returns the CLI argv (without the executable).
func BuildCLIArgs(o CLIOptions) []string {
	a := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-prompt", "stdio"}
	push := func(flag, v string) {
		if v != "" {
			a = append(a, flag, v)
		}
	}
	list := func(flag string, v []string) {
		if len(v) > 0 {
			a = append(append(a, flag), v...)
		}
	}
	push("--workspace", o.Cwd)
	push("--model", o.Model)
	push("--permission-mode", o.PermissionMode)
	push("--effort", o.Effort)
	list("--allowed-tools", o.AllowedTools)
	list("--disallowed-tools", o.DisallowedTools)
	list("--add-dir", o.AddDirs)
	list("--mcp-config", o.McpConfig)
	if o.StrictMcpConfig {
		a = append(a, "--strict-mcp-config")
	}
	if o.IgnoreRepoSettings {
		a = append(a, "--ignore-repo-settings")
	}
	if o.Settings != nil {
		b, _ := json.Marshal(o.Settings)
		push("--settings", string(b))
	}
	push("--append-system-prompt", o.AppendSystemPrompt)
	push("--system-prompt-file", o.SystemPromptFile)
	if o.MaxCostUSD > 0 {
		push("--max-cost", strconv.FormatFloat(o.MaxCostUSD, 'f', -1, 64))
	}
	if o.MaxTokens > 0 {
		push("--max-tokens", strconv.Itoa(o.MaxTokens))
	}
	push("--json-schema", o.JSONSchemaFile)
	push("--resume", o.Resume)
	if o.Continue {
		a = append(a, "--continue")
	}
	if o.ForkSession {
		a = append(a, "--fork-session")
	}
	if o.NoHooks {
		a = append(a, "--no-hooks")
	}
	if o.PermissionTimeoutSec > 0 {
		push("--permission-timeout", strconv.FormatFloat(o.PermissionTimeoutSec, 'f', -1, 64))
	}
	a = append(a, "--timeout", strconv.Itoa(o.TimeoutMs))
	return append(a, o.ExtraArgs...)
}

// ── Errors ──────────────────────────────────────────────────────────

// SchemaError: the CLI speaks a stream-json major this client does not know.
type SchemaError struct {
	SchemaVersion int
	CLIVersion    string
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("mc %s speaks stream-json schema_version %d; this client supports %d. Upgrade github.com/MonkeysCloud/monkeyscode-go (or install a matching mc)", e.CLIVersion, e.SchemaVersion, SupportedSchemaVersion)
}

// ProcessError: the CLI could not start, or exited without a result.
type ProcessError struct {
	Message    string
	ExitCode   int // -1 when it never started
	StderrTail string
	cause      error
}

// Unwrap makes errors.Is(err, ErrCLITooOld) work.
func (e *ProcessError) Unwrap() error { return e.cause }

func (e *ProcessError) Error() string {
	if e.StderrTail != "" {
		return e.Message + "\n" + e.StderrTail
	}
	return e.Message
}

// ── Session ─────────────────────────────────────────────────────────

// CLISession is a running `mc` process.
type CLISession struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	opts  CLIOptions

	mu          sync.Mutex
	sent        int
	results     int
	ending      bool
	stdinClosed bool
	interrupts  int

	sessionID  string
	lastResult *CLIResult
	err        error
	stderrTail strings.Builder
	done       chan struct{}
	exitCode   int
}

// Query starts `mc` with prompt as the first turn. With a prompt, the
// session ends after that turn's result unless keepOpen-style use is wanted:
// pass "" and call Send yourself, then Close.
func Query(ctx context.Context, prompt string, opts CLIOptions) (<-chan CLIEvent, *CLISession, error) {
	s, events, err := start(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	if prompt != "" {
		if err := s.Send(prompt); err != nil {
			return nil, nil, err
		}
		s.End()
	}
	return events, s, nil
}

// Open starts an idle session for multi-turn use: Send, read events, Send…, then End or Close.
func Open(ctx context.Context, opts CLIOptions) (<-chan CLIEvent, *CLISession, error) {
	s, events, err := start(ctx, opts)
	return events, s, err
}

func start(ctx context.Context, opts CLIOptions) (*CLISession, <-chan CLIEvent, error) {
	argv := opts.PathToMc
	if len(argv) == 0 {
		if p := os.Getenv("MC_PATH"); p != "" {
			argv = []string{p}
		} else {
			argv = []string{"mc"}
		}
	}
	cmd := exec.CommandContext(ctx, argv[0], append(append([]string{}, argv[1:]...), BuildCLIArgs(opts)...)...)
	cmd.Dir = opts.Cwd
	cmd.Env = mergeEnv(os.Environ(), opts.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, &ProcessError{Message: fmt.Sprintf("could not start %s: %v. Install the CLI (npm i -g monkeyscode-cli) or set PathToMc / MC_PATH", argv[0], err), ExitCode: -1}
	}
	s := &CLISession{cmd: cmd, stdin: stdin, opts: opts, done: make(chan struct{}), exitCode: -1}
	events := make(chan CLIEvent, 64)

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			s.mu.Lock()
			s.stderrTail.WriteString(line + "\n")
			if s.stderrTail.Len() > 8000 {
				t := s.stderrTail.String()
				s.stderrTail.Reset()
				s.stderrTail.WriteString(t[len(t)-4000:])
			}
			s.mu.Unlock()
			if opts.Stderr != nil {
				opts.Stderr(line)
			}
		}
	}()
	go func() {
		defer readers.Done()
		defer close(events)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			var head struct {
				Type      string          `json:"type"`
				RequestID string          `json:"request_id"`
				Request   json.RawMessage `json:"request"`
			}
			if json.Unmarshal(line, &head) != nil {
				if opts.Stderr != nil {
					opts.Stderr("[stdout] " + string(line))
				}
				continue
			}
			if head.Type == "control_request" {
				go s.answer(ctx, head.RequestID, head.Request)
				continue
			}
			if head.Type == "control_response" {
				continue
			}
			ev, err := ParseCLIEvent(line)
			if err != nil {
				continue
			}
			if ev.Init != nil {
				if ev.Init.SchemaVersion != SupportedSchemaVersion {
					s.setErr(&SchemaError{SchemaVersion: ev.Init.SchemaVersion, CLIVersion: ev.Init.CLIVersion})
					_ = cmd.Process.Kill()
					return
				}
				s.mu.Lock()
				s.sessionID = ev.Init.SessionID
				s.mu.Unlock()
			}
			if ev.Result != nil {
				s.mu.Lock()
				s.lastResult = ev.Result
				s.results++
				s.mu.Unlock()
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
			if ev.Result != nil {
				s.maybeCloseStdin()
			}
		}
	}()
	go func() {
		readers.Wait()
		werr := cmd.Wait()
		s.mu.Lock()
		if cmd.ProcessState != nil {
			s.exitCode = cmd.ProcessState.ExitCode()
		}
		if s.err == nil {
			switch {
			case s.sessionID == "":
				tail := strings.TrimSpace(s.stderrTail.String())
				if looksLikeOldCLI(tail) {
					s.err = &ProcessError{Message: fmt.Sprintf("mc exited (%d): this mc predates stream-json. Query/Open need mc >= %s: npm i -g monkeyscode-cli@latest", s.exitCode, MinCLIVersion), ExitCode: s.exitCode, StderrTail: tail, cause: ErrCLITooOld}
				} else {
					s.err = &ProcessError{Message: fmt.Sprintf("mc exited (%d) before system/init", s.exitCode), ExitCode: s.exitCode, StderrTail: tail}
				}
			case s.lastResult == nil:
				s.err = &ProcessError{Message: fmt.Sprintf("mc exited (%d) without a result", s.exitCode), ExitCode: s.exitCode, StderrTail: strings.TrimSpace(s.stderrTail.String())}
			case ctx.Err() != nil:
				s.err = ctx.Err()
			}
		}
		_ = werr // a non-zero exit is reported via ExitCode(), not as an error
		s.mu.Unlock()
		close(s.done)
	}()
	return s, events, nil
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := extra[k]; !ok {
			out = append(out, kv)
		}
	}
	for k, v := range extra {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func (s *CLISession) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *CLISession) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdinClosed {
		return errors.New("monkeyscode: session input is closed")
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *CLISession) answer(ctx context.Context, id string, raw json.RawMessage) {
	var req struct {
		Subtype     string         `json:"subtype"`
		ToolName    string         `json:"tool_name"`
		ToolUseID   string         `json:"tool_use_id"`
		Input       map[string]any `json:"input"`
		Description string         `json:"description"`
		Suggestions []string       `json:"suggestions"`
	}
	if json.Unmarshal(raw, &req) != nil || req.Subtype != "can_use_tool" {
		return
	}
	var r PermissionResult
	switch {
	case s.opts.CanUseTool != nil:
		r = safeDecide(ctx, s.opts.CanUseTool, req.ToolName, req.Input, PermissionContext{Description: req.Description, Suggestions: req.Suggestions, ToolUseID: req.ToolUseID})
	case s.opts.AutoApprove:
		r = Allow()
	default:
		hint := ""
		if len(req.Suggestions) > 0 {
			hint = fmt.Sprintf(" To allow it, add AllowedTools: []string{%q}.", req.Suggestions[0])
		}
		r = Deny("Not approved (headless): requires approval." + hint + " Continue without this action if possible.")
	}
	resp := map[string]any{"behavior": "deny"}
	if r.Behavior == "allow" {
		resp = map[string]any{"behavior": "allow"}
		if r.UpdatedInput != nil {
			resp["updated_input"] = r.UpdatedInput
		}
	} else if r.Message != "" {
		resp["message"] = r.Message
	}
	_ = s.write(map[string]any{"type": "control_response", "request_id": id, "response": resp})
}

// safeDecide turns a panicking callback into a deny (never an allow).
func safeDecide(ctx context.Context, f CanUseToolFunc, tool string, input map[string]any, pc PermissionContext) (r PermissionResult) {
	defer func() {
		if p := recover(); p != nil {
			r = Deny(fmt.Sprintf("Permission callback failed: %v", p))
		}
	}()
	r = f(ctx, tool, input, pc)
	if r.Behavior != "allow" && r.Behavior != "deny" {
		r = Deny("Permission callback returned no decision")
	}
	return r
}

// Send queues a user message (a new turn).
func (s *CLISession) Send(prompt string) error {
	s.mu.Lock()
	if s.ending {
		s.mu.Unlock()
		return errors.New("monkeyscode: Send after End")
	}
	s.sent++
	s.mu.Unlock()
	return s.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
}

// Interrupt stops the current turn (result subtype error_interrupted).
func (s *CLISession) Interrupt() error {
	s.mu.Lock()
	s.interrupts++
	n := s.interrupts
	s.mu.Unlock()
	return s.write(map[string]any{"type": "control_request", "request_id": fmt.Sprintf("int_%d", n), "request": map[string]any{"subtype": "interrupt"}})
}

// End: no more messages. Input closes once every sent message has a result,
// so permission answers can still be delivered.
func (s *CLISession) End() {
	s.mu.Lock()
	s.ending = true
	s.mu.Unlock()
	s.maybeCloseStdin()
}

func (s *CLISession) maybeCloseStdin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ending && !s.stdinClosed && s.results >= s.sent {
		s.stdinClosed = true
		_ = s.stdin.Close()
	}
}

// Close ends input now and waits for the process.
func (s *CLISession) Close() error {
	s.mu.Lock()
	s.ending = true
	if !s.stdinClosed {
		s.stdinClosed = true
		_ = s.stdin.Close()
	}
	s.mu.Unlock()
	return s.Wait()
}

// Wait blocks until the process exits. It returns a *SchemaError,
// *ProcessError or context error; a non-zero exit with a result is not an error (see ExitCode).
func (s *CLISession) Wait() error {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// SessionID is set once system/init arrives.
func (s *CLISession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// LastResult is the most recent turn's result (nil before the first).
func (s *CLISession) LastResult() *CLIResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResult
}

// ExitCode is the CLI's exit code after Wait (-1 before).
func (s *CLISession) ExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}
