// Package mcp exposes the graph knowledge base to the brain as a Model Context
// Protocol server over stdio (`rl graph-mcp`). It is a deliberately minimal,
// dependency-free JSON-RPC 2.0 implementation: newline-delimited messages on
// stdin/stdout, supporting initialize / tools/list / tools/call / ping — all a
// headless `claude --mcp-config` client needs.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/protonspy/ralph-loop/internal/graph"
)

const protocolVersion = "2024-11-05"

// Server dispatches MCP requests onto a graph store.
type Server struct {
	store *graph.Store
}

// NewServer wraps a store.
func NewServer(store *graph.Store) *Server { return &Server{store: store} }

// Serve reads newline-delimited JSON-RPC messages from r until EOF, writing
// responses to w. Notifications (no id) get no response, per JSON-RPC 2.0.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue // not a parseable message; MCP clients don't send batches
		}
		resp := s.Handle(req)
		if resp == nil {
			continue
		}
		out, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s\n", out); err != nil {
			return err
		}
	}
	return sc.Err()
}

// ---- JSON-RPC plumbing ----

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handle processes one message. A nil return means "no response" (notification).
func (s *Server) Handle(req request) *response {
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil // notification (e.g. notifications/initialized)
	}
	ok := func(result any) *response {
		return &response{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	switch req.Method {
	case "initialize":
		return ok(map[string]any{
			"protocolVersion": negotiateVersion(req.Params),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "rl-graph", "version": "0.1.0"},
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": toolDefs()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params"}}
		}
		return ok(s.callTool(p.Name, p.Arguments))
	default:
		return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

// negotiateVersion echoes the client's requested protocol version (the spec's
// preferred behavior when the server supports it), falling back to our default.
func negotiateVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err == nil && p.ProtocolVersion != "" {
		return p.ProtocolVersion
	}
	return protocolVersion
}

// toolResult renders a tools/call result: JSON payload as text content.
func toolResult(v any, err error) map[string]any {
	if err != nil {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		}
	}
	raw, merr := json.MarshalIndent(v, "", "  ")
	if merr != nil {
		return toolResult(nil, merr)
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(raw)}},
		"isError": false,
	}
}
