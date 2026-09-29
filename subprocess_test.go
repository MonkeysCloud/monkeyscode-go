package monkeyscode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the fake `mc`: with MC_FAKE=1 the test binary speaks
// the stream-json protocol instead of running tests (helper-process pattern).
func TestMain(m *testing.M) {
	if os.Getenv("MC_FAKE") == "1" {
		fakeMc()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeMc() {
	out := func(v any) {
		b, _ := json.Marshal(v)
		os.Stdout.Write(append(b, '\n'))
	}
	result := func(text, sub string, denials []map[string]any) {
		if denials == nil {
			denials = []map[string]any{}
		}
		out(map[string]any{"type": "result", "subtype": sub, "is_error": sub != "success", "result": text, "session_id": "sid-go",
			"num_turns": 1, "duration_ms": 1, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}, "cost_usd": 0.001,
			"is_estimated": true, "permission_denials": denials, "exit_code": map[bool]int{true: 0, false: 6}[sub == "success"]})
	}
	if log := os.Getenv("MC_FAKE_LOG"); log != "" {
		b, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(log, append(b, '\n'), 0o600)
	}
	if os.Getenv("MC_FAKE_OLD") == "1" {
		// Verbatim from the published monkeyscode-cli 0.1.12 given BuildCLIArgs' flags.
		fmt.Fprintln(os.Stderr, "error: unknown option '--output-format'")
		os.Exit(1)
	}
	schema := 1
	if os.Getenv("MC_FAKE_SCHEMA") == "2" {
		schema = 2
	}
	out(map[string]any{"type": "system", "subtype": "init", "schema_version": schema, "session_id": "sid-go", "cwd": ".", "model": "stub",
		"permission_mode": "default", "tools": []string{"create_file"}, "mcp_servers": []any{}, "cli_version": "0.0.0-fake"})
	in := bufio.NewScanner(os.Stdin)
	next := func() map[string]any {
		if !in.Scan() {
			os.Exit(0)
		}
		var m map[string]any
		_ = json.Unmarshal(in.Bytes(), &m)
		return m
	}
	n := 0
	for {
		msg := next()
		if msg["type"] != "user" {
			continue
		}
		n++
		prompt := msg["message"].(map[string]any)["content"].(string)
		out(map[string]any{"type": "user", "session_id": "sid-go", "message": map[string]any{"role": "user", "content": prompt}})
		switch {
		case strings.HasPrefix(prompt, "write "):
			path := strings.TrimPrefix(prompt, "write ")
			id := fmt.Sprintf("r%d", n)
			out(map[string]any{"type": "assistant", "session_id": "sid-go", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t", "name": "create_file", "input": map[string]any{"path": path}}}}})
			out(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "can_use_tool", "tool_name": "create_file", "input": map[string]any{"path": path}, "description": "create_file: " + path, "suggestions": []string{"create_file(" + path + ")"}}})
			var resp map[string]any
			for {
				m := next()
				if m["type"] == "control_response" && m["request_id"] == id {
					resp = m["response"].(map[string]any)
					break
				}
			}
			if resp["behavior"] == "allow" {
				used := path
				if ui, ok := resp["updated_input"].(map[string]any); ok {
					used = ui["path"].(string)
				}
				result("wrote "+used, "success", nil)
			} else {
				msg, _ := resp["message"].(string)
				result("could not write "+path, "error_permission_denied", []map[string]any{{"tool_name": "create_file", "tool_input": map[string]any{"path": path}, "reason": msg}})
			}
		case prompt == "hang":
			for {
				m := next()
				if m["type"] == "control_request" {
					out(map[string]any{"type": "control_response", "request_id": m["request_id"], "response": map[string]any{"subtype": "success"}})
					result("", "error_interrupted", nil)
					break
				}
			}
		default:
			out(map[string]any{"type": "assistant", "session_id": "sid-go", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "echo: " + prompt}}}})
			result("echo: "+prompt, "success", nil)
		}
	}
}

func fakeOpts(t *testing.T, extra map[string]string) CLIOptions {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MC_FAKE": "1"}
	for k, v := range extra {
		env[k] = v
	}
	return CLIOptions{PathToMc: []string{exe, "-test.run=^$"}, Env: env}
}

func drain(t *testing.T, events <-chan CLIEvent) []CLIEvent {
	t.Helper()
	var out []CLIEvent
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("timed out waiting for events")
		}
	}
}

func TestBuildCLIArgs(t *testing.T) {
	a := BuildCLIArgs(CLIOptions{Model: "sonnet", AllowedTools: []string{"read_file", "run_command(npm test:*)"}, MaxCostUSD: 0.5, Resume: "abc", ForkSession: true, Settings: map[string]any{"effort": "high"}})
	if !reflect.DeepEqual(a[:7], []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-prompt", "stdio"}) {
		t.Fatalf("protocol flags: %v", a[:7])
	}
	idx := func(f string) int {
		for i, v := range a {
			if v == f {
				return i
			}
		}
		t.Fatalf("missing %s in %v", f, a)
		return -1
	}
	if i := idx("--allowed-tools"); a[i+1] != "read_file" || a[i+2] != "run_command(npm test:*)" {
		t.Fatalf("allowed-tools: %v", a[i:])
	}
	if a[idx("--settings")+1] != `{"effort":"high"}` || a[idx("--max-cost")+1] != "0.5" || a[idx("--timeout")+1] != "0" {
		t.Fatalf("args: %v", a)
	}
	idx("--fork-session")
}

func TestParseCLIEvent(t *testing.T) {
	ev, err := ParseCLIEvent([]byte(`{"type":"user","session_id":"s","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"x","is_error":true}]}}`))
	if err != nil || ev.Message == nil || len(ev.Message.Blocks) != 1 || !ev.Message.Blocks[0].IsError {
		t.Fatalf("tool result: %+v %v", ev, err)
	}
	ev, _ = ParseCLIEvent([]byte(`{"type":"user","session_id":"s","message":{"role":"user","content":"hi"}}`))
	if ev.Message.Text != "hi" {
		t.Fatalf("prompt: %+v", ev.Message)
	}
	ev, _ = ParseCLIEvent([]byte(`{"type":"result","subtype":"success","result":"ok","session_id":"s","usage":{"input_tokens":3},"permission_denials":[]}`))
	if ev.Result == nil || ev.Result.Usage.InputTokens != 3 || ev.Subtype != "success" {
		t.Fatalf("result: %+v", ev.Result)
	}
}

func TestQueryOneTurn(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv.json")
	events, s, err := Query(context.Background(), "hello", fakeOpts(t, map[string]string{"MC_FAKE_LOG": log}))
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(t, events)
	if err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if evs[0].Init == nil || evs[0].Init.SessionID != "sid-go" || s.SessionID() != "sid-go" {
		t.Fatalf("init: %+v", evs[0])
	}
	last := evs[len(evs)-1]
	if last.Result == nil || last.Result.Result != "echo: hello" || s.ExitCode() != 0 {
		t.Fatalf("result: %+v exit %d", last.Result, s.ExitCode())
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), `"--permission-prompt","stdio"`) {
		t.Fatalf("argv: %s", b)
	}
}

func TestMultiTurnAllowDenyRewrite(t *testing.T) {
	var asked []string
	opts := fakeOpts(t, nil)
	opts.CanUseTool = func(ctx context.Context, tool string, input map[string]any, pc PermissionContext) PermissionResult {
		p := input["path"].(string)
		asked = append(asked, tool+":"+p+":"+pc.Suggestions[0])
		switch p {
		case "yes.txt":
			return Allow()
		case "raw.txt":
			return AllowWith(map[string]any{"path": "cooked.txt"})
		}
		return Deny("not that one")
	}
	events, s, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var results []*CLIResult
	for _, p := range []string{"write yes.txt", "write no.txt", "write raw.txt"} {
		if err := s.Send(p); err != nil {
			t.Fatal(err)
		}
		for ev := range events {
			if ev.Result != nil {
				results = append(results, ev.Result)
				break
			}
		}
	}
	s.End()
	drain(t, events)
	if err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	got := []string{results[0].Result, results[1].Result, results[2].Result}
	if !reflect.DeepEqual(got, []string{"wrote yes.txt", "could not write no.txt", "wrote cooked.txt"}) {
		t.Fatalf("results: %v", got)
	}
	if results[1].PermissionDenials[0].Reason != "not that one" {
		t.Fatalf("denial: %+v", results[1].PermissionDenials)
	}
	if len(asked) != 3 || asked[0] != "create_file:yes.txt:create_file(yes.txt)" {
		t.Fatalf("asked: %v", asked)
	}
}

func TestPanickingCallbackDenies(t *testing.T) {
	opts := fakeOpts(t, nil)
	opts.CanUseTool = func(context.Context, string, map[string]any, PermissionContext) PermissionResult { panic("bug") }
	events, s, err := Query(context.Background(), "write x.txt", opts)
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(t, events)
	_ = s.Wait()
	r := evs[len(evs)-1].Result
	if r == nil || r.Subtype != "error_permission_denied" || !strings.Contains(r.PermissionDenials[0].Reason, "Permission callback failed: bug") {
		t.Fatalf("result: %+v", r)
	}
}

func TestNoCallbackDeniesWithHint(t *testing.T) {
	events, s, err := Query(context.Background(), "write x.txt", fakeOpts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(t, events)
	_ = s.Wait()
	if r := evs[len(evs)-1].Result; r == nil || !strings.Contains(r.PermissionDenials[0].Reason, `AllowedTools: []string{"create_file(x.txt)"}`) {
		t.Fatalf("result: %+v", r)
	}
}

func TestInterrupt(t *testing.T) {
	events, s, err := Open(context.Background(), fakeOpts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Send("hang")
	_ = s.Interrupt()
	var r *CLIResult
	for ev := range events {
		if ev.Result != nil {
			r = ev.Result
			break
		}
	}
	_ = s.Close()
	if r == nil || r.Subtype != "error_interrupted" {
		t.Fatalf("result: %+v", r)
	}
}

func TestUnknownSchemaRefused(t *testing.T) {
	events, s, err := Query(context.Background(), "x", fakeOpts(t, map[string]string{"MC_FAKE_SCHEMA": "2"}))
	if err != nil {
		t.Fatal(err)
	}
	drain(t, events)
	var se *SchemaError
	if err := s.Wait(); !errors.As(err, &se) || se.SchemaVersion != 2 || !strings.Contains(err.Error(), "Upgrade") {
		t.Fatalf("want SchemaError, got %v", err)
	}
}

func TestOldCLIExplainsUpgrade(t *testing.T) {
	events, s, err := Query(context.Background(), "x", fakeOpts(t, map[string]string{"MC_FAKE_OLD": "1"}))
	if err != nil {
		// The old CLI can exit before the prompt is written; that is fine too.
		if !errors.Is(err, ErrCLITooOld) && !strings.Contains(err.Error(), "closed") && !strings.Contains(err.Error(), "broken pipe") {
			t.Fatal(err)
		}
		if s == nil {
			return
		}
	}
	if events != nil {
		drain(t, events)
	}
	err = s.Wait()
	if !errors.Is(err, ErrCLITooOld) {
		t.Fatalf("want ErrCLITooOld, got %v", err)
	}
	var pe *ProcessError
	if !errors.As(err, &pe) || pe.ExitCode != 1 || !strings.Contains(pe.Message, "mc >= "+MinCLIVersion) || !strings.Contains(pe.Message, "monkeyscode-cli@latest") {
		t.Fatalf("message should name the floor and the upgrade: %v", err)
	}
}

func TestHostedRun404PointsToQuery(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewClient(Options{APIKey: "k", ProxyURL: srv.URL})
	_, err := c.Run(context.Background(), "x")
	if !errors.Is(err, ErrHostedRunUnavailable) || !strings.Contains(err.Error(), "Query/Open") {
		t.Fatalf("want ErrHostedRunUnavailable pointing at Query/Open, got %v", err)
	}
}

func TestVersionsAgree(t *testing.T) {
	if userAgent != "monkeyscode-sdk-go/"+Version || telemetrySDKVersion != Version {
		t.Fatalf("version drift: ua=%q telemetry=%q Version=%q", userAgent, telemetrySDKVersion, Version)
	}
	for _, v := range []string{Version, MinCLIVersion} {
		if parts := strings.Split(v, "."); len(parts) != 3 {
			t.Fatalf("%q is not x.y.z (the sync workflow parses it)", v)
		}
	}
}

func TestMissingBinary(t *testing.T) {
	_, _, err := Query(context.Background(), "x", CLIOptions{PathToMc: []string{filepath.Join(t.TempDir(), "nope-mc")}})
	var pe *ProcessError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "npm i -g monkeyscode-cli") {
		t.Fatalf("want ProcessError with install hint, got %v", err)
	}
}
