package monkeyscode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const defaultAdminBaseURL = "https://api.monkeyscode.com"

// AdminClient provides access to the Admin API for team management.
type AdminClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// AdminOptions configures an AdminClient.
type AdminOptions struct {
	APIKey  string
	BaseURL string
}

// NewAdminClient creates a new admin API client.
func NewAdminClient(opts AdminOptions) *AdminClient {
	base := opts.BaseURL
	if base == "" {
		base = defaultAdminBaseURL
	}
	return &AdminClient{
		apiKey:     opts.APIKey,
		baseURL:    base,
		httpClient: &http.Client{},
	}
}

// UsageQuery filters usage results.
type UsageQuery struct {
	Period string // "7d", "30d", "90d", "all"
	User   string
	Model  string
}

// GetUsage returns usage report for the organization.
func (c *AdminClient) GetUsage(ctx context.Context, q UsageQuery) (map[string]any, error) {
	params := url.Values{}
	if q.Period != "" {
		params.Set("period", q.Period)
	}
	if q.User != "" {
		params.Set("user", q.User)
	}
	if q.Model != "" {
		params.Set("model", q.Model)
	}
	return c.get(ctx, "/v1/admin/usage", params)
}

// RunQuery filters run list results.
type RunQuery struct {
	Limit  int
	Cursor string
	Status string
	User   string
	Model  string
}

// ListRuns lists agent runs across the organization.
func (c *AdminClient) ListRuns(ctx context.Context, q RunQuery) (map[string]any, error) {
	params := url.Values{}
	if q.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.Cursor != "" {
		params.Set("cursor", q.Cursor)
	}
	if q.Status != "" {
		params.Set("status", q.Status)
	}
	if q.User != "" {
		params.Set("user", q.User)
	}
	if q.Model != "" {
		params.Set("model", q.Model)
	}
	return c.get(ctx, "/v1/admin/runs", params)
}

// GetActiveAgents returns currently running agents.
func (c *AdminClient) GetActiveAgents(ctx context.Context) (map[string]any, error) {
	return c.get(ctx, "/v1/admin/agents", nil)
}

// ListMembers returns team members.
func (c *AdminClient) ListMembers(ctx context.Context) (map[string]any, error) {
	return c.get(ctx, "/v1/admin/members", nil)
}

func (c *AdminClient) get(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("admin API error: %d %s", resp.StatusCode, resp.Status)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}
