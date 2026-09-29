package monkeyscode

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CIExitCodes defines standard exit codes for CI/CD.
var CIExitCodes = struct {
	Success      int
	AgentError   int
	BudgetExceed int
	Timeout      int
	AuthError    int
	ConfigError  int
}{
	Success:      0,
	AgentError:   1,
	BudgetExceed: 2,
	Timeout:      3,
	AuthError:    4,
	ConfigError:  5,
}

// CIFinding is a single issue found by the agent.
type CIFinding struct {
	Severity  string // "error", "warning", "note"
	Message   string
	File      string
	Line      int
	EndLine   int
	Column    int
	EndColumn int
	RuleID    string
}

// SARIFReport represents a SARIF 2.1.0 report.
type SARIFReport struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []SARIFRun `json:"runs"`
}

// SARIFRun is a single run in a SARIF report.
type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

// SARIFTool describes the tool that produced the results.
type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

// SARIFDriver is the tool driver information.
type SARIFDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []SARIFRule `json:"rules,omitempty"`
}

// SARIFRule describes a finding rule.
type SARIFRule struct {
	ID               string          `json:"id"`
	ShortDescription SARIFMessage    `json:"shortDescription"`
	DefaultConfig    SARIFRuleConfig `json:"defaultConfiguration,omitempty"`
}

// SARIFRuleConfig is the default configuration for a rule.
type SARIFRuleConfig struct {
	Level string `json:"level"`
}

// SARIFResult is a single finding in SARIF format.
type SARIFResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   SARIFMessage    `json:"message"`
	Locations []SARIFLocation `json:"locations,omitempty"`
}

// SARIFMessage is a text message.
type SARIFMessage struct {
	Text string `json:"text"`
}

// SARIFLocation is the location of a finding.
type SARIFLocation struct {
	PhysicalLocation SARIFPhysicalLocation `json:"physicalLocation"`
}

// SARIFPhysicalLocation is the physical file location.
type SARIFPhysicalLocation struct {
	ArtifactLocation SARIFArtifact `json:"artifactLocation"`
	Region           *SARIFRegion  `json:"region,omitempty"`
}

// SARIFArtifact identifies a file.
type SARIFArtifact struct {
	URI string `json:"uri"`
}

// SARIFRegion is a region within a file.
type SARIFRegion struct {
	StartLine   int `json:"startLine,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// ToSARIF converts a list of findings to a SARIF 2.1.0 report.
func ToSARIF(findings []CIFinding) *SARIFReport {
	// Deduplicate rules
	ruleMap := make(map[string]SARIFRule)
	var results []SARIFResult

	for _, f := range findings {
		ruleID := f.RuleID
		if ruleID == "" {
			ruleID = "monkeyscode-finding"
		}

		if _, exists := ruleMap[ruleID]; !exists {
			ruleMap[ruleID] = SARIFRule{
				ID:               ruleID,
				ShortDescription: SARIFMessage{Text: ruleID},
				DefaultConfig:    SARIFRuleConfig{Level: mapSeverity(f.Severity)},
			}
		}

		result := SARIFResult{
			RuleID:  ruleID,
			Level:   mapSeverity(f.Severity),
			Message: SARIFMessage{Text: f.Message},
		}

		if f.File != "" {
			loc := SARIFLocation{
				PhysicalLocation: SARIFPhysicalLocation{
					ArtifactLocation: SARIFArtifact{URI: f.File},
				},
			}
			if f.Line > 0 {
				loc.PhysicalLocation.Region = &SARIFRegion{
					StartLine:   f.Line,
					EndLine:     f.EndLine,
					StartColumn: f.Column,
					EndColumn:   f.EndColumn,
				}
			}
			result.Locations = []SARIFLocation{loc}
		}

		results = append(results, result)
	}

	rules := make([]SARIFRule, 0, len(ruleMap))
	for _, r := range ruleMap {
		rules = append(rules, r)
	}

	return &SARIFReport{
		Schema:  "https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0-rtm.6.json",
		Version: "2.1.0",
		Runs: []SARIFRun{{
			Tool: SARIFTool{
				Driver: SARIFDriver{
					Name:    "MonkeysCode",
					Version: Version,
					Rules:   rules,
				},
			},
			Results: results,
		}},
	}
}

// ToSARIFJSON converts findings to a SARIF JSON string.
func ToSARIFJSON(findings []CIFinding) (string, error) {
	report := ToSARIF(findings)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ToGitHubAnnotations formats findings as GitHub Actions annotations.
func ToGitHubAnnotations(findings []CIFinding) []string {
	var annotations []string
	for _, f := range findings {
		level := "warning"
		switch f.Severity {
		case "error", "high", "critical":
			level = "error"
		case "note", "low":
			level = "notice"
		}

		if f.File != "" && f.Line > 0 {
			annotations = append(annotations,
				fmt.Sprintf("::%s file=%s,line=%d::%s", level, f.File, f.Line, f.Message))
		} else {
			annotations = append(annotations,
				fmt.Sprintf("::%s::%s", level, f.Message))
		}
	}
	return annotations
}

// CISummary formats a Result as a CI-friendly summary string.
func CISummary(result *Result) string {
	status := "✅ PASSED"
	if !result.Success {
		status = "❌ FAILED"
	}

	lines := []string{
		fmt.Sprintf("## MonkeysCode Agent — %s", status),
		"",
		fmt.Sprintf("- **Model**: %s", result.Model),
		fmt.Sprintf("- **Duration**: %s", formatDuration(float64(result.DurationMs))),
		fmt.Sprintf("- **Cost**: $%.4f", result.Cost),
		fmt.Sprintf("- **Tokens**: %d", result.Tokens.Total),
	}

	if len(result.FilesChanged) > 0 {
		lines = append(lines, fmt.Sprintf("- **Files Changed**: %d", len(result.FilesChanged)))
	}

	if result.Summary != "" {
		lines = append(lines, "", "### Summary", "", result.Summary)
	}

	return strings.Join(lines, "\n")
}

func mapSeverity(s string) string {
	switch strings.ToLower(s) {
	case "error", "high", "critical":
		return "error"
	case "warning", "medium":
		return "warning"
	case "note", "low", "info":
		return "note"
	default:
		return "warning"
	}
}

func formatDuration(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.0fms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", ms/1000)
	}
	mins := int(ms / 60000)
	secs := int(ms) % 60000 / 1000
	return fmt.Sprintf("%dm %ds", mins, secs)
}

// CITimestamp returns a formatted timestamp for CI output.
func CITimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
