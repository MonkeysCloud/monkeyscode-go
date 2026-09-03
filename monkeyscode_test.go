package monkeyscode_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	monkeyscode "github.com/MonkeysCloud/monkeyscode-go"
)

func TestNewClientRequiresAPIKey(t *testing.T) {
	_, err := monkeyscode.NewClient(&monkeyscode.Config{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewClientWithAPIKey(t *testing.T) {
	c, err := monkeyscode.NewClient(&monkeyscode.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := monkeyscode.DefaultConfig()
	if cfg.ProxyURL != "https://api.monkeyscode.com" {
		t.Errorf("expected default ProxyURL, got %s", cfg.ProxyURL)
	}
	if cfg.Model != "capuchin-reason" {
		t.Errorf("expected default model, got %s", cfg.Model)
	}
	if cfg.MaxTurns != 50 {
		t.Errorf("expected 50 max turns, got %d", cfg.MaxTurns)
	}
}

func TestAgentRunWithMockServer(t *testing.T) {
	// Mock SSE server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/run" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}

		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key-123" {
			t.Errorf("unexpected auth: %s", auth)
		}

		// Verify request body
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if body["prompt"] != "write a hello world" {
			t.Errorf("unexpected prompt: %v", body["prompt"])
		}

		// Send SSE response
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)

		events := []monkeyscode.Event{
			{Type: monkeyscode.EventText, Content: "I'll create a hello world file."},
			{Type: monkeyscode.EventToolCall, Tool: "write_file", Args: map[string]any{"path": "hello.py"}},
			{Type: monkeyscode.EventToolResult, Tool: "write_file", Result: "File written"},
			{
				Type:       monkeyscode.EventComplete,
				Summary:    "Created hello.py",
				DurationMs: 1500,
				Tokens:     &monkeyscode.TokenUsage{Input: 100, Output: 50, Total: 150},
				Cost:       0.002,
				Files: []monkeyscode.FileChange{
					{Path: "hello.py", Action: "created", LinesAdded: 3},
				},
			},
		}

		for _, ev := range events {
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}))
	defer server.Close()

	client, err := monkeyscode.NewClient(&monkeyscode.Config{
		APIKey:   "test-key-123",
		ProxyURL: server.URL,
		Model:    "capuchin-flash",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	agent := client.Agent("/tmp/test-workspace")
	result, err := agent.Run(context.Background(), "write a hello world")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got failure: %s", result.Summary)
	}
	if result.Summary != "Created hello.py" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
	if result.Cost != 0.002 {
		t.Errorf("unexpected cost: %f", result.Cost)
	}
	if len(result.FilesChanged) != 1 {
		t.Errorf("expected 1 file changed, got %d", len(result.FilesChanged))
	}
	if result.Tokens.Total != 150 {
		t.Errorf("expected 150 total tokens, got %d", result.Tokens.Total)
	}
}

func TestAgentStreamEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)

		events := []monkeyscode.Event{
			{Type: monkeyscode.EventText, Content: "Hello"},
			{Type: monkeyscode.EventText, Content: " World"},
			{Type: monkeyscode.EventComplete, Summary: "Done"},
		}
		for _, ev := range events {
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}))
	defer server.Close()

	client, _ := monkeyscode.NewClient(&monkeyscode.Config{
		APIKey:   "key",
		ProxyURL: server.URL,
	})

	var types []monkeyscode.EventType
	for ev := range client.Agent("").Stream(context.Background(), "hello") {
		types = append(types, ev.Type)
	}

	if len(types) != 3 {
		t.Fatalf("expected 3 events, got %d", len(types))
	}
	if types[0] != monkeyscode.EventText {
		t.Errorf("expected text event first, got %s", types[0])
	}
	if types[2] != monkeyscode.EventComplete {
		t.Errorf("expected complete event last, got %s", types[2])
	}
}

func TestAgentRunHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, 401)
	}))
	defer server.Close()

	client, _ := monkeyscode.NewClient(&monkeyscode.Config{
		APIKey:   "bad-key",
		ProxyURL: server.URL,
	})

	_, err := client.Agent("").Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestSessionIDTracking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)

		data, _ := json.Marshal(map[string]any{
			"type":      "complete",
			"summary":   "done",
			"sessionId": "sess-abc-123",
		})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}))
	defer server.Close()

	client, _ := monkeyscode.NewClient(&monkeyscode.Config{
		APIKey:   "key",
		ProxyURL: server.URL,
	})

	agent := client.Agent("")
	_, _ = agent.Run(context.Background(), "test")
	if agent.SessionID() != "sess-abc-123" {
		t.Errorf("expected session ID 'sess-abc-123', got '%s'", agent.SessionID())
	}
}
