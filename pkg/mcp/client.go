package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 helpers
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// ---------------------------------------------------------------------------
// stdioConn wraps a single child process speaking JSON-RPC over stdio.
// ---------------------------------------------------------------------------

type stdioConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex // serialises writes
	nextID atomic.Int64
}

func (c *stdioConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}
	data = append(data, '\n')

	// Write under lock.
	c.mu.Lock()
	_, err = c.stdin.Write(data)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("mcp: write to stdin: %w", err)
	}

	// Read the next line (blocking). In a production system we would
	// demultiplex by id; here we assume sequential request/response.
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := c.reader.ReadBytes('\n')
		ch <- readResult{line, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case rr := <-ch:
		if rr.err != nil {
			return nil, fmt.Errorf("mcp: read from stdout: %w", rr.err)
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(rr.line, &resp); err != nil {
			return nil, fmt.Errorf("mcp: unmarshal response: %w", err)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *stdioConn) close() error {
	_ = c.stdin.Close()
	return c.cmd.Process.Kill()
}

// ---------------------------------------------------------------------------
// McpClientManager
// ---------------------------------------------------------------------------

// McpClientManager manages connections to multiple MCP servers.
type McpClientManager struct {
	configs map[string]McpServerConfig

	mu       sync.RWMutex
	conns    map[string]*stdioConn
	statuses map[string]*McpConnectionStatus
}

// NewMcpClientManager creates a manager from a name -> config mapping.
func NewMcpClientManager(configs map[string]McpServerConfig) *McpClientManager {
	return &McpClientManager{
		configs:  configs,
		conns:    make(map[string]*stdioConn),
		statuses: make(map[string]*McpConnectionStatus),
	}
}

// ConnectAll establishes connections to all configured stdio servers.
// Non-stdio transports are marked as pending (not yet supported).
func (m *McpClientManager) ConnectAll(ctx context.Context) error {
	for name, cfg := range m.configs {
		switch c := cfg.(type) {
		case *McpStdioServerConfig:
			if err := m.connectStdio(ctx, name, c); err != nil {
				m.setStatus(name, StateFailed, cfg.TransportType(), err.Error(), nil, nil)
			}
		default:
			m.setStatus(name, StatePending, cfg.TransportType(), "transport not yet implemented", nil, nil)
		}
	}
	return nil
}

// Close shuts down all active connections.
func (m *McpClientManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, conn := range m.conns {
		_ = conn.close()
		delete(m.conns, name)
	}
}

// ListStatuses returns the current connection status for every configured server.
func (m *McpClientManager) ListStatuses() []McpConnectionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]McpConnectionStatus, 0, len(m.statuses))
	for _, s := range m.statuses {
		out = append(out, *s)
	}
	return out
}

// ListTools returns the aggregate list of tools from all connected servers.
func (m *McpClientManager) ListTools() []McpToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []McpToolInfo
	for _, s := range m.statuses {
		if s.State == StateConnected {
			out = append(out, s.Tools...)
		}
	}
	return out
}

// ListResources returns the aggregate list of resources from all connected servers.
func (m *McpClientManager) ListResources() []McpResourceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []McpResourceInfo
	for _, s := range m.statuses {
		if s.State == StateConnected {
			out = append(out, s.Resources...)
		}
	}
	return out
}

// CallTool invokes a tool on the named server and returns the text result.
func (m *McpClientManager) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (string, error) {
	conn, err := m.getConn(serverName)
	if err != nil {
		return "", err
	}

	params := map[string]any{
		"name":      toolName,
		"arguments": arguments,
	}
	raw, err := conn.call(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("mcp: call_tool %s/%s: %w", serverName, toolName, err)
	}

	// MCP tools/call returns {content: [{type:"text", text:"..."}]}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw), nil
	}
	var texts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	return joinStrings(texts, "\n"), nil
}

// ReadResource reads a resource from the named server.
func (m *McpClientManager) ReadResource(ctx context.Context, serverName, uri string) (string, error) {
	conn, err := m.getConn(serverName)
	if err != nil {
		return "", err
	}

	params := map[string]any{
		"uri": uri,
	}
	raw, err := conn.call(ctx, "resources/read", params)
	if err != nil {
		return "", fmt.Errorf("mcp: read_resource %s/%s: %w", serverName, uri, err)
	}

	var result struct {
		Contents []struct {
			Text string `json:"text"`
			URI  string `json:"uri"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw), nil
	}
	var texts []string
	for _, c := range result.Contents {
		texts = append(texts, c.Text)
	}
	return joinStrings(texts, "\n"), nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (m *McpClientManager) getConn(serverName string) (*stdioConn, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conn, ok := m.conns[serverName]
	if !ok {
		return nil, fmt.Errorf("mcp: server %q not connected", serverName)
	}
	return conn, nil
}

func (m *McpClientManager) setStatus(name string, state ConnectionState, transport, detail string, tools []McpToolInfo, resources []McpResourceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tools == nil {
		tools = []McpToolInfo{}
	}
	if resources == nil {
		resources = []McpResourceInfo{}
	}
	m.statuses[name] = &McpConnectionStatus{
		Name:      name,
		State:     state,
		Detail:    detail,
		Transport: transport,
		Tools:     tools,
		Resources: resources,
	}
}

func (m *McpClientManager) connectStdio(ctx context.Context, name string, cfg *McpStdioServerConfig) error {
	args := cfg.Args
	cmd := exec.CommandContext(ctx, cfg.Command, args...)
	if cfg.Cwd != nil {
		cmd.Dir = *cfg.Cwd
	}

	// Merge environment.
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	// Discard stderr to avoid blocking.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	conn := &stdioConn{
		cmd:    cmd,
		stdin:  stdinPipe,
		reader: bufio.NewReader(stdoutPipe),
	}

	// --- MCP protocol: initialize ---
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "openharness",
			"version": "0.1.0",
		},
	}
	_, err = conn.call(ctx, "initialize", initParams)
	if err != nil {
		_ = conn.close()
		return fmt.Errorf("initialize: %w", err)
	}

	// Send initialized notification (no id, no response expected).
	notif := jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}
	notifData, _ := json.Marshal(notif)
	notifData = append(notifData, '\n')
	conn.mu.Lock()
	_, _ = conn.stdin.Write(notifData)
	conn.mu.Unlock()

	// --- list tools ---
	tools, err := m.listRemoteTools(ctx, conn, name)
	if err != nil {
		_ = conn.close()
		return fmt.Errorf("list_tools: %w", err)
	}

	// --- list resources ---
	resources, err := m.listRemoteResources(ctx, conn, name)
	if err != nil {
		// Resources are optional; proceed with empty list.
		resources = nil
	}

	m.mu.Lock()
	m.conns[name] = conn
	m.mu.Unlock()

	m.setStatus(name, StateConnected, "stdio", "", tools, resources)
	return nil
}

func (m *McpClientManager) listRemoteTools(ctx context.Context, conn *stdioConn, serverName string) ([]McpToolInfo, error) {
	raw, err := conn.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]McpToolInfo, len(resp.Tools))
	for i, t := range resp.Tools {
		out[i] = McpToolInfo{
			ServerName:  serverName,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return out, nil
}

func (m *McpClientManager) listRemoteResources(ctx context.Context, conn *stdioConn, serverName string) ([]McpResourceInfo, error) {
	raw, err := conn.call(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Resources []struct {
			Name        string `json:"name"`
			URI         string `json:"uri"`
			Description string `json:"description"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]McpResourceInfo, len(resp.Resources))
	for i, r := range resp.Resources {
		out[i] = McpResourceInfo{
			ServerName:  serverName,
			Name:        r.Name,
			URI:         r.URI,
			Description: r.Description,
		}
	}
	return out, nil
}

// joinStrings is a tiny helper to avoid importing strings just for Join.
func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
