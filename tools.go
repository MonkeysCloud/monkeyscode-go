package monkeyscode

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolDefinition defines a custom tool that the agent can invoke.
type ToolDefinition struct {
	// Name is the unique tool identifier (e.g., "deploy", "send_email").
	Name string

	// Description is shown to the agent for tool selection.
	Description string

	// Parameters is a JSON Schema describing the tool's inputs.
	// Example: map[string]any{"env": map[string]any{"type": "string", "enum": []string{"staging", "prod"}}}
	Parameters map[string]any

	// Execute runs the tool with the given arguments and returns a result.
	Execute func(ctx context.Context, args map[string]any) (any, error)
}

// ToolResult wraps the output of a custom tool execution.
type ToolResult struct {
	Tool    string `json:"tool"`
	Result  string `json:"result"`
	Success bool   `json:"success"`
}

// ToJSONSchema converts a ToolDefinition to the JSON Schema format
// expected by the model proxy.
func (t *ToolDefinition) ToJSONSchema() map[string]any {
	return map[string]any{
		"name":        t.Name,
		"description": t.Description,
		"parameters": map[string]any{
			"type":       "object",
			"properties": t.Parameters,
		},
	}
}

// ToolRegistry manages custom tool registration.
type ToolRegistry struct {
	tools map[string]*ToolDefinition
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]*ToolDefinition)}
}

// Add registers a custom tool.
func (r *ToolRegistry) Add(tool *ToolDefinition) {
	r.tools[tool.Name] = tool
}

// Remove unregisters a custom tool by name.
func (r *ToolRegistry) Remove(name string) {
	delete(r.tools, name)
}

// Get returns a tool by name, or nil if not found.
func (r *ToolRegistry) Get(name string) *ToolDefinition {
	return r.tools[name]
}

// Has returns true if a tool with the given name is registered.
func (r *ToolRegistry) Has(name string) bool {
	_, ok := r.tools[name]
	return ok
}

// List returns the names of all registered tools.
func (r *ToolRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	return len(r.tools)
}

// ToJSONSchemas converts all registered tools to JSON Schema format.
func (r *ToolRegistry) ToJSONSchemas() []map[string]any {
	schemas := make([]map[string]any, 0, len(r.tools))
	for _, tool := range r.tools {
		schemas = append(schemas, tool.ToJSONSchema())
	}
	return schemas
}

// Execute runs a tool by name with the given arguments.
func (r *ToolRegistry) Execute(ctx context.Context, name string, rawArgs string) ToolResult {
	tool := r.tools[name]
	if tool == nil {
		return ToolResult{Tool: name, Result: fmt.Sprintf("unknown tool: %s", name), Success: false}
	}

	var args map[string]any
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return ToolResult{Tool: name, Result: fmt.Sprintf("invalid args: %v", err), Success: false}
		}
	}
	if args == nil {
		args = make(map[string]any)
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		return ToolResult{Tool: name, Result: err.Error(), Success: false}
	}

	// Marshal result to string
	switch v := result.(type) {
	case string:
		return ToolResult{Tool: name, Result: v, Success: true}
	default:
		data, err := json.Marshal(result)
		if err != nil {
			return ToolResult{Tool: name, Result: fmt.Sprintf("%v", result), Success: true}
		}
		return ToolResult{Tool: name, Result: string(data), Success: true}
	}
}

// DefineTool is a convenience function to create a ToolDefinition.
func DefineTool(name, description string, params map[string]any, execute func(ctx context.Context, args map[string]any) (any, error)) *ToolDefinition {
	return &ToolDefinition{
		Name:        name,
		Description: description,
		Parameters:  params,
		Execute:     execute,
	}
}
