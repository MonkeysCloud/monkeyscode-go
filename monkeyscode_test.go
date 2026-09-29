package monkeyscode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Client Tests ────────────────────────────────────────────────────

func TestNewClient_defaults(t *testing.T) {
	c := NewClient(Options{APIKey: "test_key"})

	if c.apiKey != "test_key" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "test_key")
	}
	if c.model != "auto" {
		t.Errorf("model = %q, want %q", c.model, "auto")
	}
	if c.proxyURL != DefaultProxyURL {
		t.Errorf("proxyURL = %q, want %q", c.proxyURL, DefaultProxyURL)
	}
	if c.maxTurns != DefaultMaxTurns {
		t.Errorf("maxTurns = %d, want %d", c.maxTurns, DefaultMaxTurns)
	}
}

func TestNewClient_customOptions(t *testing.T) {
	c := NewClient(Options{
		APIKey:           "custom_key",
		Model:            "capuchin-reason",
		ProxyURL:         "https://custom.proxy.com/",
		WorkingDirectory: "/tmp/test",
		MaxTurns:         50,
		Timeout:          10 * time.Minute,
		PermissionsMode:  "autoApprove",
		MaxCost:          5.00,
	})

	if c.model != "capuchin-reason" {
		t.Errorf("model = %q", c.model)
	}
	if c.proxyURL != "https://custom.proxy.com" {
		t.Errorf("proxyURL trailing slash not trimmed: %q", c.proxyURL)
	}
	if c.maxCost != 5.00 {
		t.Errorf("maxCost = %f", c.maxCost)
	}
}

func TestClient_Run_success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []map[string]any{
			{"type": "text", "content": "Fixing errors..."},
			{"type": "complete", "summary": "Fixed 3 errors", "tokens": map[string]any{"input": float64(100), "output": float64(50), "total": float64(150)}, "cost": 0.0025},
		}

		flusher := w.(http.Flusher)
		for _, e := range events {
			data, _ := json.Marshal(e)
			w.Write([]byte("data: " + string(data) + "\n\n"))
			flusher.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	c := NewClient(Options{APIKey: "test_key", ProxyURL: server.URL})
	result, err := c.Run(context.Background(), "Fix errors")
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if result.Summary != "Fixed 3 errors" {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.Cost != 0.0025 {
		t.Errorf("Cost = %f", result.Cost)
	}
}

func TestClient_Run_httpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid api key"))
	}))
	defer server.Close()

	c := NewClient(Options{APIKey: "bad_key", ProxyURL: server.URL})
	_, err := c.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_Stream_events(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []map[string]any{
			{"type": "text", "content": "Hello"},
			{"type": "tool_call", "tool": "file_read", "args": "{}"},
			{"type": "tool_result", "tool": "file_read", "result": "contents"},
			{"type": "complete", "summary": "Done", "tokens": map[string]any{"total": float64(100)}, "cost": 0.001},
		}

		flusher := w.(http.Flusher)
		for _, e := range events {
			data, _ := json.Marshal(e)
			w.Write([]byte("data: " + string(data) + "\n\n"))
			flusher.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	c := NewClient(Options{APIKey: "test", ProxyURL: server.URL})
	var types []EventType
	for event := range c.Stream(context.Background(), "test") {
		types = append(types, event.Type)
	}

	expected := []EventType{EventText, EventToolCall, EventToolResult, EventComplete}
	if len(types) != len(expected) {
		t.Fatalf("got %d events, want %d", len(types), len(expected))
	}
	for i, typ := range types {
		if typ != expected[i] {
			t.Errorf("event[%d] = %q, want %q", i, typ, expected[i])
		}
	}
}

// ── Tool Tests ──────────────────────────────────────────────────────

func TestToolRegistry(t *testing.T) {
	reg := NewToolRegistry()

	deploy := DefineTool("deploy", "Deploy app", map[string]any{
		"env": map[string]any{"type": "string"},
	}, func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]string{"deployed": "true"}, nil
	})

	reg.Add(deploy)

	if !reg.Has("deploy") {
		t.Error("expected deploy to be registered")
	}
	if reg.Count() != 1 {
		t.Errorf("Count = %d", reg.Count())
	}

	schema := deploy.ToJSONSchema()
	if schema["name"] != "deploy" {
		t.Errorf("schema name = %v", schema["name"])
	}

	result := reg.Execute(context.Background(), "deploy", `{"env": "staging"}`)
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Result)
	}

	result = reg.Execute(context.Background(), "unknown_tool", "")
	if result.Success {
		t.Error("expected failure for unknown tool")
	}

	reg.Remove("deploy")
	if reg.Has("deploy") {
		t.Error("expected deploy to be removed")
	}
}

func TestClientToolRegistration(t *testing.T) {
	c := NewClient(Options{APIKey: "test"})
	c.AddTool(DefineTool("test_tool", "test", nil, nil))
	tools := c.ListTools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	c.RemoveTool("test_tool")
	if len(c.ListTools()) != 0 {
		t.Error("expected 0 tools after removal")
	}
}

// ── Hook Manager Tests ──────────────────────────────────────────────

func TestHookManager_PreHooks_Block(t *testing.T) {
	hm := NewHookManager()
	order := []string{}

	hm.AddPreHook(func(ctx PreToolContext) PreToolResult {
		order = append(order, "h1")
		return PreToolResult{Action: HookAllow}
	})
	hm.AddPreHook(func(ctx PreToolContext) PreToolResult {
		order = append(order, "h2")
		return PreToolResult{Action: HookBlock, Reason: "blocked"}
	})
	hm.AddPreHook(func(ctx PreToolContext) PreToolResult {
		order = append(order, "h3")
		return PreToolResult{Action: HookAllow}
	})

	result := hm.RunPreHooks(PreToolContext{Tool: "test", Args: map[string]any{}})
	if result.Action != HookBlock {
		t.Errorf("Action = %q, want block", result.Action)
	}
	if result.Reason != "blocked" {
		t.Errorf("Reason = %q", result.Reason)
	}
	if len(order) != 2 || order[1] != "h2" {
		t.Errorf("order = %v, expected [h1 h2]", order)
	}
}

func TestHookManager_PreHooks_Modify(t *testing.T) {
	hm := NewHookManager()
	hm.AddPreHook(func(ctx PreToolContext) PreToolResult {
		return PreToolResult{Action: HookModify, Args: map[string]any{"extra": true}}
	})

	result := hm.RunPreHooks(PreToolContext{Tool: "test", Args: map[string]any{"original": true}})
	if result.Action != HookModify {
		t.Errorf("Action = %q", result.Action)
	}
	if result.Args["extra"] != true || result.Args["original"] != true {
		t.Errorf("Args = %v", result.Args)
	}
}

func TestHookManager_PostHooks_PanicRecovery(t *testing.T) {
	hm := NewHookManager()
	called := false

	hm.AddPostHook(func(ctx PostToolContext) {
		panic("boom")
	})
	hm.AddPostHook(func(ctx PostToolContext) {
		called = true
	})

	hm.RunPostHooks(PostToolContext{Tool: "test"})
	if !called {
		t.Error("second post-hook should still run")
	}
}

func TestHookManager_HasHooks(t *testing.T) {
	hm := NewHookManager()
	if hm.HasHooks() {
		t.Error("should not have hooks")
	}
	hm.AddPreHook(func(ctx PreToolContext) PreToolResult { return PreToolResult{Action: HookAllow} })
	if !hm.HasHooks() {
		t.Error("should have hooks")
	}
	hm.ClearAll()
	if hm.HasHooks() {
		t.Error("should not have hooks after clear")
	}
}

func TestClientHookRegistration(t *testing.T) {
	c := NewClient(Options{APIKey: "test"})
	c.OnPreToolUse(func(ctx PreToolContext) PreToolResult { return PreToolResult{Action: HookAllow} })
	c.OnPostToolUse(func(ctx PostToolContext) {})
	if !c.hookManagerHasHooks() {
		t.Error("should have hooks")
	}
	c.ClearHooks()
	if c.hookManagerHasHooks() {
		t.Error("should not have hooks after clear")
	}
}

// ── Session Tests ───────────────────────────────────────────────────

func TestSessionManager(t *testing.T) {
	c := NewClient(Options{APIKey: "test"})
	sm := c.Sessions()

	sm.Resume("ses_123")
	if sm.Current() != "ses_123" {
		t.Errorf("Current = %q", sm.Current())
	}

	sm.Clear()
	if sm.Current() != "" {
		t.Error("expected empty after clear")
	}
}

func TestCompactSession(t *testing.T) {
	s := &Session{ID: "ses_123", Model: "capuchin-reason", RunCount: 3, TotalCost: 0.125}
	result := CompactSession(s)
	if !strings.Contains(result, "ses_123") || !strings.Contains(result, "$0.1250") {
		t.Errorf("unexpected format: %s", result)
	}
}

// ── Subagent Tests ──────────────────────────────────────────────────

func TestCapSummary_short(t *testing.T) {
	result := CapSummary("hello", 100)
	if result != "hello" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestCapSummary_truncate(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence."
	result := CapSummary(text, 35)
	if !strings.Contains(result, "First sentence.") {
		t.Errorf("expected sentence boundary cut: %q", result)
	}
	if !strings.Contains(result, "[summary truncated]") {
		t.Errorf("expected truncation marker: %q", result)
	}
}

// ── CI Tests ────────────────────────────────────────────────────────

func TestToSARIF(t *testing.T) {
	findings := []CIFinding{
		{Severity: "error", Message: "SQL injection", File: "db.go", Line: 42, RuleID: "security-sql"},
		{Severity: "warning", Message: "Unused var", File: "app.go", Line: 10},
	}

	report := ToSARIF(findings)
	if report.Version != "2.1.0" {
		t.Errorf("Version = %q", report.Version)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(report.Runs))
	}
	if len(report.Runs[0].Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(report.Runs[0].Results))
	}
	if report.Runs[0].Results[0].Level != "error" {
		t.Errorf("first level = %q", report.Runs[0].Results[0].Level)
	}
}

func TestToSARIFJSON(t *testing.T) {
	findings := []CIFinding{{Severity: "note", Message: "test"}}
	jsonStr, err := ToSARIFJSON(findings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonStr, "2.1.0") {
		t.Error("expected SARIF version in JSON")
	}
}

func TestToGitHubAnnotations(t *testing.T) {
	findings := []CIFinding{
		{Severity: "error", Message: "Bug found", File: "main.go", Line: 5},
		{Severity: "warning", Message: "Style issue"},
	}
	annotations := ToGitHubAnnotations(findings)
	if len(annotations) != 2 {
		t.Fatalf("expected 2, got %d", len(annotations))
	}
	if !strings.HasPrefix(annotations[0], "::error") {
		t.Errorf("expected ::error prefix: %q", annotations[0])
	}
	if !strings.HasPrefix(annotations[1], "::warning") {
		t.Errorf("expected ::warning prefix: %q", annotations[1])
	}
}

func TestCISummary(t *testing.T) {
	result := &Result{Success: true, Model: "capuchin", Cost: 0.01, Tokens: TokenUsage{Total: 100}, Summary: "Done"}
	summary := CISummary(result)
	if !strings.Contains(summary, "✅ PASSED") {
		t.Error("expected PASSED")
	}
	if !strings.Contains(summary, "$0.0100") {
		t.Error("expected cost")
	}
}

// ── Export Tests ─────────────────────────────────────────────────────

func TestToMarkdown(t *testing.T) {
	result := &Result{Success: true, Model: "capuchin", Summary: "Fixed bugs", Cost: 0.05, Tokens: TokenUsage{Input: 100, Output: 50, Total: 150}}
	md := ToMarkdown(result)
	if !strings.Contains(md, "# MonkeysCode Agent Report") {
		t.Error("missing header")
	}
	if !strings.Contains(md, "✅ Success") {
		t.Error("missing status")
	}
	if !strings.Contains(md, "$0.0500") {
		t.Error("missing cost")
	}
}

func TestToHTML(t *testing.T) {
	result := &Result{Success: true, Summary: "OK", Model: "test"}
	html := ToHTML(result)
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("missing DOCTYPE")
	}
	if !strings.Contains(html, "var(--bg)") {
		t.Error("missing CSS vars")
	}
}

func TestToTranscript(t *testing.T) {
	result := &Result{Success: true, Status: "completed", Model: "test", Cost: 0.01}
	transcript := ToTranscript(result)
	if transcript.Version != "1.0" {
		t.Errorf("Version = %q", transcript.Version)
	}
	if !transcript.Result.Success {
		t.Error("expected success")
	}
}

// ── OTEL Tests ──────────────────────────────────────────────────────

func TestOtelExporter_nil(t *testing.T) {
	e := NewOtelExporter(OtelOptions{})
	if e != nil {
		t.Error("expected nil for empty endpoint")
	}
}

func TestOtelExporter_create(t *testing.T) {
	e := NewOtelExporter(OtelOptions{Endpoint: "http://localhost:4318"})
	if e == nil {
		t.Fatal("expected non-nil exporter")
	}
	if !strings.HasSuffix(e.endpoint, "/v1/traces") {
		t.Errorf("endpoint = %q", e.endpoint)
	}
	if e.TraceID() == "" {
		t.Error("expected non-empty trace ID")
	}
}

func TestOtelExporter_recordEvent(t *testing.T) {
	e := NewOtelExporter(OtelOptions{Endpoint: "http://localhost:4318"})
	e.RecordEvent(Event{Type: EventToolCall, Tool: "file_read"})
	e.RecordEvent(Event{Type: EventToolResult, Tool: "file_read"})
	e.RecordEvent(Event{Type: EventComplete, Cost: 0.01, Tokens: TokenUsage{Total: 100}, DurationMs: 1000})

	if e.SpanCount() != 2 { // tool span + run span
		t.Errorf("SpanCount = %d, want 2", e.SpanCount())
	}
}

// ── Run Persistence Tests ───────────────────────────────────────────

func TestRunStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	run := &RunRecord{
		ID:        "run_123",
		Prompt:    "test",
		Model:     "capuchin",
		Status:    "completed",
		Summary:   "Done",
		Cost:      0.01,
		CreatedAt: time.Now(),
	}

	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load("run_123")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != "run_123" {
		t.Errorf("ID = %q", loaded.ID)
	}

	// Prefix match
	loaded, err = store.Load("run_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != "run_123" {
		t.Errorf("prefix match: ID = %q", loaded.ID)
	}

	// List
	runs, err := store.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("List = %d", len(runs))
	}

	// Delete
	if err := store.Delete("run_123"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Load("run_123")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestRunStore_SaveResult(t *testing.T) {
	dir := t.TempDir()
	store, err := NewRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	result := &Result{Success: true, Status: "completed", Model: "test", Cost: 0.01}
	run, err := store.SaveResult("test prompt", result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(run.ID, "run_") {
		t.Errorf("ID = %q", run.ID)
	}

	// Verify it was saved
	_, err = os.ReadFile(filepath.Join(dir, run.ID+".json"))
	if err != nil {
		t.Error("file not saved")
	}
}

// ── Sandbox Tests ───────────────────────────────────────────────────

func TestDefaultSandboxOptions(t *testing.T) {
	opts := DefaultSandboxOptions()
	if opts.Mode != SandboxLocal {
		t.Errorf("Mode = %q", opts.Mode)
	}
	if opts.Tier != TierSmall {
		t.Errorf("Tier = %q", opts.Tier)
	}
	if opts.Image == "" {
		t.Error("expected default image")
	}
}

func TestIsDockerAvailable(t *testing.T) {
	// Just ensure it doesn't panic
	_ = IsDockerAvailable()
}

// ── Admin Tests ─────────────────────────────────────────────────────

func TestNewAdminClient(t *testing.T) {
	c := NewAdminClient(AdminOptions{APIKey: "admin_key", BaseURL: "https://custom.api.com"})
	if c.apiKey != "admin_key" {
		t.Errorf("apiKey = %q", c.apiKey)
	}
}

// ── Orchestrator Tests ──────────────────────────────────────────────

func TestRunParallel_emptyPrompts(t *testing.T) {
	c := NewClient(Options{APIKey: "test"})
	_, err := RunParallel(context.Background(), c, ParallelOptions{})
	if err == nil {
		t.Error("expected error for empty prompts")
	}
}

func TestRunFanOut_emptyTargets(t *testing.T) {
	c := NewClient(Options{APIKey: "test"})
	_, err := RunFanOut(context.Background(), c, FanOutOptions{Prompt: "test"})
	if err == nil {
		t.Error("expected error for empty targets")
	}
}

func TestSemaphore(t *testing.T) {
	sem := NewSemaphore(2)
	sem.Acquire()
	sem.Acquire()
	if sem.TryAcquire() {
		t.Error("should not acquire when full")
	}
	sem.Release()
	if !sem.TryAcquire() {
		t.Error("should acquire after release")
	}
}

// ── Daemon Tests ────────────────────────────────────────────────────

func TestNewDaemonClient(t *testing.T) {
	c := NewDaemonClient(DaemonClientOptions{URL: "ws://localhost:9120"})
	if c.url != "http://localhost:9120" {
		t.Errorf("URL not normalized: %q", c.url)
	}
	if c.Connected() {
		t.Error("should not be connected initially")
	}
}

func TestDaemonClient_defaults(t *testing.T) {
	c := NewDaemonClient(DaemonClientOptions{})
	if c.url != "http://localhost:9120" {
		t.Errorf("default URL: %q", c.url)
	}
}

func TestDaemonClient_disconnect(t *testing.T) {
	c := NewDaemonClient(DaemonClientOptions{})
	c.connected = true
	c.Disconnect()
	if c.Connected() {
		t.Error("should be disconnected")
	}
}

// ── MCP Tests ───────────────────────────────────────────────────────

func TestNewMcpManager(t *testing.T) {
	m := NewMcpManager()
	if m.ServerCount() != 0 {
		t.Error("expected 0 servers")
	}
	if len(m.ServerNames()) != 0 {
		t.Error("expected empty names")
	}
}

func TestMcpManager_AllTools_empty(t *testing.T) {
	m := NewMcpManager()
	tools := m.AllTools()
	if len(tools) != 0 {
		t.Error("expected 0 tools")
	}
}

func TestMcpManager_ListTools_not_connected(t *testing.T) {
	m := NewMcpManager()
	_, err := m.ListTools("postgres")
	if err == nil {
		t.Error("expected error for unknown server")
	}
}

func TestMcpManager_Status_not_connected(t *testing.T) {
	m := NewMcpManager()
	status := m.Status("unknown")
	if status.Connected {
		t.Error("should not be connected")
	}
}

func TestMcpManager_Disconnect_noop(t *testing.T) {
	m := NewMcpManager()
	// Should not panic
	m.Disconnect("nonexistent")
	m.DisconnectAll()
}

// ── Watcher Tests ───────────────────────────────────────────────────

func TestBuildSnapshot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "nested.go"), []byte("package main"), 0o644)

	ignores := map[string]bool{"node_modules": true}
	snapshot := buildSnapshot(dir, ignores)
	if len(snapshot) != 2 {
		t.Errorf("expected 2 files, got %d", len(snapshot))
	}
}

func TestDiffSnapshots(t *testing.T) {
	old := map[string]int64{"a.go": 100, "b.go": 200}
	new := map[string]int64{"a.go": 100, "b.go": 300, "c.go": 400}

	changed := diffSnapshots(old, new)
	hasB, hasC := false, false
	for _, c := range changed {
		if c == "b.go" {
			hasB = true
		}
		if c == "c.go" {
			hasC = true
		}
	}
	if !hasB || !hasC {
		t.Errorf("expected b.go and c.go in changed: %v", changed)
	}
}

func TestShouldIgnore(t *testing.T) {
	ignores := map[string]bool{"node_modules": true}
	if !shouldIgnore("node_modules", ignores) {
		t.Error("should ignore node_modules")
	}
	if !shouldIgnore(".git", ignores) {
		t.Error("should ignore dotfiles")
	}
	if shouldIgnore(".env", ignores) {
		t.Error("should not ignore .env")
	}
	if shouldIgnore("main.go", ignores) {
		t.Error("should not ignore main.go")
	}
}

func TestFormatChangeBatch(t *testing.T) {
	result := FormatChangeBatch(nil)
	if result != "No changes" {
		t.Errorf("expected 'No changes', got %q", result)
	}

	result = FormatChangeBatch([]string{"src/main.go"})
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected file in output: %q", result)
	}
}

// ── Telemetry Tests ─────────────────────────────────────────────────

func TestTelemetryCollector_disabled(t *testing.T) {
	tc := NewTelemetryCollector(TelemetryOptions{Enabled: false})
	tc.RecordRunStart("test")
	if tc.BufferSize() != 0 {
		t.Error("should not buffer when disabled")
	}
}

func TestTelemetryCollector_enabled(t *testing.T) {
	tc := NewTelemetryCollector(TelemetryOptions{Enabled: true})
	defer tc.Close()

	tc.RecordRunStart("capuchin")
	if tc.BufferSize() != 1 {
		t.Errorf("buffer = %d, want 1", tc.BufferSize())
	}

	tc.RecordError("capuchin", "TIMEOUT")
	if tc.BufferSize() != 2 {
		t.Errorf("buffer = %d, want 2", tc.BufferSize())
	}

	tc.RecordRunComplete("capuchin", map[string]int64{"input": 100}, 1000, nil)
	if tc.BufferSize() != 3 {
		t.Errorf("buffer = %d, want 3", tc.BufferSize())
	}
}

func TestTelemetryCollector_close(t *testing.T) {
	tc := NewTelemetryCollector(TelemetryOptions{Enabled: true})
	tc.RecordRunStart("test")
	tc.Close()
	// After close, buffer should be flushed (endpoint unreachable, so events dropped silently)
}
