package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/protonspy/ralph-loop/internal/graph"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	store, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(store)
}

func call(t *testing.T, s *Server, method string, params any) *response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return s.Handle(request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: raw})
}

// callTool invokes tools/call and returns the text payload, failing on isError.
func callTool(t *testing.T, s *Server, name string, args any) string {
	t.Helper()
	resp := call(t, s, "tools/call", map[string]any{"name": name, "arguments": args})
	if resp.Error != nil {
		t.Fatalf("%s: rpc error %+v", name, resp.Error)
	}
	res := resp.Result.(map[string]any)
	text := res["content"].([]map[string]any)[0]["text"].(string)
	if res["isError"] == true {
		t.Fatalf("%s: tool error: %s", name, text)
	}
	return text
}

func TestInitializeAndToolsList(t *testing.T) {
	s := testServer(t)
	resp := call(t, s, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize should echo the client version: %s", raw)
	}

	resp = call(t, s, "tools/list", nil)
	raw, _ = json.Marshal(resp.Result)
	for _, tool := range []string{"add_fact", "add_episode", "search_facts", "neighbors", "facts_as_of"} {
		if !strings.Contains(string(raw), `"name":"`+tool+`"`) {
			t.Errorf("tools/list missing %s", tool)
		}
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	s := testServer(t)
	if resp := s.Handle(request{JSONRPC: "2.0", Method: "notifications/initialized"}); resp != nil {
		t.Fatalf("notification must not be answered, got %+v", resp)
	}
}

func TestAddFactSearchNeighbors(t *testing.T) {
	s := testServer(t)

	ep := callTool(t, s, "add_episode", map[string]any{
		"name": "phaser docs", "source": "context7", "content": "Phaser 3 uses a Scene lifecycle."})
	var epOut struct{ UUID string }
	if err := json.Unmarshal([]byte(ep), &epOut); err != nil || epOut.UUID == "" {
		t.Fatalf("add_episode returned %q", ep)
	}

	out := callTool(t, s, "add_fact", map[string]any{
		"src_kind": "concept", "src_name": "phaser", "dst_kind": "concept", "dst_name": "scene lifecycle",
		"rel": "RELATES_TO", "fact": "Phaser 3 organiza o jogo em Scenes", "episode": epOut.UUID})
	if !strings.Contains(out, "RELATES_TO") {
		t.Fatalf("add_fact output: %s", out)
	}

	if out = callTool(t, s, "search_facts", map[string]any{"query": "scenes"}); !strings.Contains(out, "Phaser 3") {
		t.Fatalf("search_facts output: %s", out)
	}
	if out = callTool(t, s, "neighbors", map[string]any{"kind": "concept", "name": "phaser", "depth": 2}); !strings.Contains(out, "RELATES_TO") {
		t.Fatalf("neighbors output: %s", out)
	}
}

func TestToolErrorIsSoft(t *testing.T) {
	s := testServer(t)
	resp := call(t, s, "tools/call", map[string]any{"name": "neighbors", "arguments": map[string]any{"kind": "feat", "name": "ghost"}})
	if resp.Error != nil {
		t.Fatalf("tool failures must be isError results, not rpc errors: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if res["isError"] != true {
		t.Fatalf("expected isError result, got %+v", res)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := testServer(t)
	resp := call(t, s, "resources/list", nil)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("want -32601, got %+v", resp)
	}
}
