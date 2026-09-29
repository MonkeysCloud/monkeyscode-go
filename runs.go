package monkeyscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunRecord is a persisted run for later review or resume.
type RunRecord struct {
	ID          string     `json:"id"`
	Prompt      string     `json:"prompt"`
	Model       string     `json:"model"`
	SessionID   string     `json:"sessionId"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	Cost        float64    `json:"cost"`
	Tokens      TokenUsage `json:"tokens"`
	DurationMs  int64      `json:"durationMs"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt time.Time  `json:"completedAt,omitempty"`
}

// RunStore manages persistent run records.
type RunStore struct {
	dir string
}

// NewRunStore creates a run store at the given directory.
// The directory is created if it doesn't exist.
func NewRunStore(dir string) (*RunStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create run store dir: %w", err)
	}
	return &RunStore{dir: dir}, nil
}

// DefaultRunStore creates a run store in ~/.monkeyscode/runs.
func DefaultRunStore() (*RunStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewRunStore(filepath.Join(home, ".monkeyscode", "runs"))
}

// Save persists a run record.
func (s *RunStore) Save(run *RunRecord) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}

	path := filepath.Join(s.dir, run.ID+".json")
	return os.WriteFile(path, data, 0o644)
}

// Load retrieves a run by full or prefix ID.
func (s *RunStore) Load(idOrPrefix string) (*RunRecord, error) {
	// Try exact match first
	path := filepath.Join(s.dir, idOrPrefix+".json")
	data, err := os.ReadFile(path)
	if err == nil {
		var run RunRecord
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		return &run, nil
	}

	// Try prefix match
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read run store: %w", err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), idOrPrefix) && strings.HasSuffix(entry.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
			if err != nil {
				continue
			}
			var run RunRecord
			if err := json.Unmarshal(data, &run); err != nil {
				continue
			}
			return &run, nil
		}
	}

	return nil, fmt.Errorf("run not found: %s", idOrPrefix)
}

// List returns recent runs, sorted by creation time (newest first).
func (s *RunStore) List(limit int) ([]RunRecord, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var runs []RunRecord
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var run RunRecord
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		runs = append(runs, run)
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})

	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}

	return runs, nil
}

// Delete removes a run record.
func (s *RunStore) Delete(id string) error {
	return os.Remove(filepath.Join(s.dir, id+".json"))
}

// SaveResult creates a RunRecord from a Result and saves it.
func (s *RunStore) SaveResult(prompt string, result *Result) (*RunRecord, error) {
	run := &RunRecord{
		ID:          fmt.Sprintf("run_%d", time.Now().UnixMilli()),
		Prompt:      prompt,
		Model:       result.Model,
		SessionID:   result.SessionID,
		Status:      result.Status,
		Summary:     result.Summary,
		Cost:        result.Cost,
		Tokens:      result.Tokens,
		DurationMs:  result.DurationMs,
		CreatedAt:   time.Now(),
		CompletedAt: time.Now(),
	}

	if err := s.Save(run); err != nil {
		return nil, err
	}
	return run, nil
}
