package monkeyscode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// McpServerConfig configures an MCP server connection.
type McpServerConfig struct {
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string
	Timeout int // seconds, default 30
}

// McpTool is an MCP tool descriptor.
type McpTool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// McpToolResult is the result from calling an MCP tool.
type McpToolResult struct {
	Content []map[string]any
	IsError bool
}

// McpServerStatus shows the state of an MCP server.
type McpServerStatus struct {
	Name      string
	Pid       int
	Connected bool
	ToolCount int
}

type mcpConnection struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	tools  []McpTool
	mu     sync.Mutex
	reqID  int
}

// McpManager manages MCP server processes and tool invocations.
type McpManager struct {
	mu      sync.RWMutex
	servers map[string]*mcpConnection
}

// NewMcpManager creates a new MCP manager.
func NewMcpManager() *McpManager {
	return &McpManager{servers: make(map[string]*mcpConnection)}
}

// ServerNames returns the names of all connected servers.
func (m *McpManager) ServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	return names
}

// ServerCount returns the number of connected servers.
func (m *McpManager) ServerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.servers)
}

// Connect launches and connects to an MCP server.
func (m *McpManager) Connect(ctx context.Context, name string, config McpServerConfig) (*McpServerStatus, error) {
	cmd := exec.CommandContext(ctx, config.Command, config.Args...)

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	if config.Cwd != "" {
		cmd.Dir = config.Cwd
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server: %w", err)
	}

	conn := &mcpConnection{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}

	// Initialize
	_, err = conn.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "monkeyscode-sdk-go", "version": Version},
	})
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	conn.notify("notifications/initialized", nil)

	// Discover tools
	toolsResult, err := conn.request("tools/list", nil)
	if err == nil {
		if result, ok := toolsResult.(map[string]any); ok {
			if toolsList, ok := result["tools"].([]any); ok {
				for _, raw := range toolsList {
					if t, ok := raw.(map[string]any); ok {
						conn.tools = append(conn.tools, McpTool{
							Name:        strVal(t, "name"),
							Description: strVal(t, "description"),
						})
					}
				}
			}
		}
	}

	m.mu.Lock()
	m.servers[name] = conn
	m.mu.Unlock()

	return &McpServerStatus{
		Name:      name,
		Pid:       cmd.Process.Pid,
		Connected: true,
		ToolCount: len(conn.tools),
	}, nil
}

// Disconnect kills an MCP server.
func (m *McpManager) Disconnect(name string) {
	m.mu.Lock()
	conn := m.servers[name]
	delete(m.servers, name)
	m.mu.Unlock()

	if conn != nil && conn.cmd.Process != nil {
		conn.cmd.Process.Kill()
	}
}

// DisconnectAll kills all MCP servers.
func (m *McpManager) DisconnectAll() {
	m.mu.Lock()
	for name, conn := range m.servers {
		if conn.cmd.Process != nil {
			conn.cmd.Process.Kill()
		}
		delete(m.servers, name)
	}
	m.mu.Unlock()
}

// ListTools returns tools from a named server.
func (m *McpManager) ListTools(name string) ([]McpTool, error) {
	m.mu.RLock()
	conn, ok := m.servers[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server not connected: %s", name)
	}
	return conn.tools, nil
}

// CallTool invokes a tool on a named server.
func (m *McpManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*McpToolResult, error) {
	m.mu.RLock()
	conn, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server not connected: %s", serverName)
	}

	result, err := conn.request("tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}

	mcpResult := &McpToolResult{}
	if m, ok := result.(map[string]any); ok {
		if content, ok := m["content"].([]any); ok {
			for _, raw := range content {
				if item, ok := raw.(map[string]any); ok {
					mcpResult.Content = append(mcpResult.Content, item)
				}
			}
		}
		if isError, ok := m["isError"].(bool); ok {
			mcpResult.IsError = isError
		}
	}

	return mcpResult, nil
}

// AllTools returns tools from all connected servers.
func (m *McpManager) AllTools() []McpTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var tools []McpTool
	for _, conn := range m.servers {
		tools = append(tools, conn.tools...)
	}
	return tools
}

// Status returns the status of a named server.
func (m *McpManager) Status(name string) McpServerStatus {
	m.mu.RLock()
	conn, ok := m.servers[name]
	m.mu.RUnlock()
	if !ok {
		return McpServerStatus{Name: name, Connected: false}
	}
	pid := 0
	if conn.cmd.Process != nil {
		pid = conn.cmd.Process.Pid
	}
	return McpServerStatus{
		Name:      name,
		Pid:       pid,
		Connected: conn.cmd.ProcessState == nil,
		ToolCount: len(conn.tools),
	}
}

// ── Internal JSON-RPC ───────────────────────────────────────────────

func (c *mcpConnection) request(method string, params map[string]any) (any, error) {
	c.mu.Lock()
	c.reqID++
	id := c.reqID
	c.mu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write to MCP: %w", err)
	}

	// Read response line
	buf := make([]byte, 64*1024)
	n, err := c.stdout.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read from MCP: %w", err)
	}

	// Find the JSON line
	line := strings.TrimSpace(string(buf[:n]))
	// May contain multiple lines, find the one with our ID
	for _, l := range strings.Split(line, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(l), &resp); err != nil {
			continue
		}
		if errVal, ok := resp["error"]; ok {
			return nil, fmt.Errorf("MCP error: %v", errVal)
		}
		return resp["result"], nil
	}

	return nil, fmt.Errorf("no response from MCP server")
}

func (c *mcpConnection) notify(method string, params map[string]any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(msg)
	c.stdin.Write(append(data, '\n'))
}
