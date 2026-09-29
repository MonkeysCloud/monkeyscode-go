package monkeyscode

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OtelOptions configures OpenTelemetry export.
type OtelOptions struct {
	// Endpoint is the OTLP HTTP endpoint (e.g., "http://localhost:4318").
	Endpoint string

	// Headers are additional HTTP headers (e.g., authentication).
	Headers map[string]string

	// ServiceName overrides the default service name.
	ServiceName string

	// Debug enables debug logging.
	Debug bool
}

// OtelExporter sends traces to an OTLP HTTP endpoint.
type OtelExporter struct {
	endpoint    string
	headers     map[string]string
	serviceName string
	debug       bool
	httpClient  *http.Client

	mu      sync.Mutex
	spans   []otelSpan
	traceID string
	pending map[string]*otelSpan // tool name → open span
}

type otelSpan struct {
	TraceID      string `json:"traceId"`
	SpanID       string `json:"spanId"`
	ParentSpanID string `json:"parentSpanId,omitempty"`
	Name         string `json:"name"`
	Kind         int    `json:"kind"`
	StartTimeNs  int64  `json:"startTimeUnixNano"`
	EndTimeNs    int64  `json:"endTimeUnixNano"`
	Status       struct {
		Code int `json:"code"`
	} `json:"status"`
	Attributes []otelAttr `json:"attributes,omitempty"`
}

type otelAttr struct {
	Key   string    `json:"key"`
	Value otelValue `json:"value"`
}

type otelValue struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    int64  `json:"intValue,omitempty"`
}

// NewOtelExporter creates an exporter, or nil if no endpoint.
func NewOtelExporter(opts OtelOptions) *OtelExporter {
	endpoint := opts.Endpoint
	if endpoint == "" {
		return nil
	}

	if !strings.HasSuffix(endpoint, "/v1/traces") {
		endpoint = strings.TrimRight(endpoint, "/") + "/v1/traces"
	}

	serviceName := opts.ServiceName
	if serviceName == "" {
		serviceName = "monkeyscode-sdk-go"
	}

	return &OtelExporter{
		endpoint:    endpoint,
		headers:     opts.Headers,
		serviceName: serviceName,
		debug:       opts.Debug,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		traceID:     generateTraceID(),
		pending:     make(map[string]*otelSpan),
	}
}

// RecordEvent records an agent event as an OTEL span.
func (e *OtelExporter) RecordEvent(event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now().UnixNano()

	switch event.Type {
	case EventToolCall:
		span := &otelSpan{
			TraceID:     e.traceID,
			SpanID:      generateSpanID(),
			Name:        fmt.Sprintf("tool.%s", event.Tool),
			Kind:        3, // CLIENT
			StartTimeNs: now,
			Attributes: []otelAttr{
				{Key: "tool.name", Value: otelValue{StringValue: event.Tool}},
			},
		}
		e.pending[event.Tool] = span

	case EventToolResult:
		if span, ok := e.pending[event.Tool]; ok {
			span.EndTimeNs = now
			if event.Err != nil {
				span.Status.Code = 2 // ERROR
			}
			e.spans = append(e.spans, *span)
			delete(e.pending, event.Tool)
		}

	case EventComplete:
		// Record the overall run as a root span
		e.spans = append(e.spans, otelSpan{
			TraceID:     e.traceID,
			SpanID:      generateSpanID(),
			Name:        "agent.run",
			Kind:        2, // SERVER
			StartTimeNs: now - event.DurationMs*1e6,
			EndTimeNs:   now,
			Attributes: []otelAttr{
				{Key: "agent.cost", Value: otelValue{StringValue: fmt.Sprintf("%.4f", event.Cost)}},
				{Key: "agent.tokens.total", Value: otelValue{IntValue: event.Tokens.Total}},
			},
		})
	}
}

// Flush sends all accumulated spans to the OTLP endpoint.
func (e *OtelExporter) Flush() error {
	e.mu.Lock()
	spans := make([]otelSpan, len(e.spans))
	copy(spans, e.spans)
	e.spans = nil
	e.mu.Unlock()

	if len(spans) == 0 {
		return nil
	}

	payload := map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": []otelAttr{
					{Key: "service.name", Value: otelValue{StringValue: e.serviceName}},
				},
			},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{"name": "monkeyscode-sdk-go", "version": Version},
				"spans": spans,
			}},
		}},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal otel payload: %w", err)
	}

	req, err := http.NewRequest("POST", e.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create otel request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send otel traces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("otel endpoint returned %d", resp.StatusCode)
	}

	return nil
}

// SpanCount returns the number of accumulated (unflushed) spans.
func (e *OtelExporter) SpanCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.spans)
}

// TraceID returns the current trace ID.
func (e *OtelExporter) TraceID() string {
	return e.traceID
}

func generateTraceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateSpanID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
