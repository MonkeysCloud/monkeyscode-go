package monkeyscode

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// TelemetryOptions configures opt-in telemetry.
type TelemetryOptions struct {
	Endpoint        string
	Enabled         bool
	Metadata        map[string]string
	FlushIntervalMs int
}

// TelemetryEvent is an anonymous usage event.
type TelemetryEvent struct {
	Type        string            `json:"type"` // "run_start" | "run_complete" | "run_error"
	Timestamp   string            `json:"timestamp"`
	SDKVersion  string            `json:"sdkVersion"`
	Model       string            `json:"model"`
	EventCounts map[string]int    `json:"eventCounts,omitempty"`
	Tokens      map[string]int64  `json:"tokens,omitempty"`
	DurationMs  int64             `json:"durationMs,omitempty"`
	ErrorCode   string            `json:"errorCode,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

const telemetrySDKVersion = Version

// TelemetryCollector collects and sends anonymous usage data.
type TelemetryCollector struct {
	options    TelemetryOptions
	mu         sync.Mutex
	buffer     []TelemetryEvent
	httpClient *http.Client
	done       chan struct{}
}

// NewTelemetryCollector creates a telemetry collector.
func NewTelemetryCollector(opts TelemetryOptions) *TelemetryCollector {
	if opts.Endpoint == "" {
		opts.Endpoint = "https://telemetry.monkeyscode.com/v1/events"
	}
	if opts.FlushIntervalMs <= 0 {
		opts.FlushIntervalMs = 30000
	}

	tc := &TelemetryCollector{
		options:    opts,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		done:       make(chan struct{}),
	}

	if opts.Enabled {
		go tc.flushLoop()
	}

	return tc
}

// RecordRunStart records a run start event.
func (tc *TelemetryCollector) RecordRunStart(model string) {
	if !tc.options.Enabled {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.buffer = append(tc.buffer, TelemetryEvent{
		Type:       "run_start",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		SDKVersion: telemetrySDKVersion,
		Model:      model,
		Metadata:   tc.options.Metadata,
	})
}

// RecordRunComplete records a run completion event.
func (tc *TelemetryCollector) RecordRunComplete(model string, tokens map[string]int64, durationMs int64, eventCounts map[string]int) {
	if !tc.options.Enabled {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.buffer = append(tc.buffer, TelemetryEvent{
		Type:        "run_complete",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		SDKVersion:  telemetrySDKVersion,
		Model:       model,
		Tokens:      tokens,
		DurationMs:  durationMs,
		EventCounts: eventCounts,
		Metadata:    tc.options.Metadata,
	})
}

// RecordError records a run error (code only, no PII).
func (tc *TelemetryCollector) RecordError(model, errorCode string) {
	if !tc.options.Enabled {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.buffer = append(tc.buffer, TelemetryEvent{
		Type:       "run_error",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		SDKVersion: telemetrySDKVersion,
		Model:      model,
		ErrorCode:  errorCode,
		Metadata:   tc.options.Metadata,
	})
}

// Flush sends buffered events.
func (tc *TelemetryCollector) Flush() {
	tc.mu.Lock()
	if len(tc.buffer) == 0 {
		tc.mu.Unlock()
		return
	}
	events := make([]TelemetryEvent, len(tc.buffer))
	copy(events, tc.buffer)
	tc.buffer = nil
	tc.mu.Unlock()

	payload := map[string]any{"events": events}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", tc.options.Endpoint, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// Close stops collecting and flushes remaining events.
func (tc *TelemetryCollector) Close() {
	select {
	case <-tc.done:
	default:
		close(tc.done)
	}
	tc.Flush()
}

// BufferSize returns the number of buffered events.
func (tc *TelemetryCollector) BufferSize() int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return len(tc.buffer)
}

func (tc *TelemetryCollector) flushLoop() {
	ticker := time.NewTicker(time.Duration(tc.options.FlushIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-tc.done:
			return
		case <-ticker.C:
			tc.Flush()
		}
	}
}
