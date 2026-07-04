// Package tool runs an AI coding assistant as a fresh, stateless process — one
// invocation per loop iteration. The prompt is piped on stdin; output is echoed
// live to stderr and captured for the caller (e.g. completion detection).
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner spawns a specific AI tool. It carries no state between Run calls —
// that is the whole point of the fresh-context methodology.
type Runner struct {
	Name string // "claude" (default) or "amp"
}

// Command returns the argv for this tool. The prompt arrives on stdin.
func (r Runner) Command() ([]string, error) {
	switch r.Name {
	case "", "claude":
		// --print: non-interactive, print the final result and exit.
		return []string{"claude", "--dangerously-skip-permissions", "--print"}, nil
	case "amp":
		return []string{"amp", "--dangerously-allow-all"}, nil
	default:
		return nil, fmt.Errorf("unknown tool %q (want claude|amp)", r.Name)
	}
}

// Run executes one fresh iteration, feeding prompt on stdin. It returns the
// combined captured output. A non-zero exit is returned as err but the captured
// output is still valid — like ralph.sh, a tool failure should not kill the loop.
// extraArgs append tool flags (e.g. --mcp-config, --agent) after the base argv.
func (r Runner) Run(ctx context.Context, dir, prompt string, extraArgs ...string) (string, error) {
	argv, err := r.Command()
	if err != nil {
		return "", err
	}
	argv = append(argv, extraArgs...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	cmd.Stderr = io.MultiWriter(&buf, os.Stderr)

	err = cmd.Run()
	return buf.String(), err
}

// envelope is the wrapper `claude -p --output-format json` prints around the
// model's answer.
type envelope struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"` // the model's raw text answer
}

// RunJSON is a structured brain activation: it runs the tool in headless JSON
// mode, feeding prompt on stdin, and returns the model's raw answer text (the
// envelope's `result`). extraArgs let a caller add flags like --agent or
// --mcp-config. Only claude is supported for structured output today.
func (r Runner) RunJSON(ctx context.Context, dir, prompt string, extraArgs ...string) (string, error) {
	if r.Name != "" && r.Name != "claude" {
		return "", fmt.Errorf("structured (JSON) activation only supports claude, not %q", r.Name)
	}
	argv := append([]string{"claude", "-p", "--output-format", "json", "--dangerously-skip-permissions"}, extraArgs...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &env); err != nil {
		return "", fmt.Errorf("parse claude json envelope: %w", err)
	}
	if env.IsError {
		return "", fmt.Errorf("claude returned an error result: %s", env.Result)
	}
	return env.Result, nil
}

// ExtractJSON strips ```json code fences (and surrounding prose is not expected)
// so the caller can json.Unmarshal the model's answer.
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	// fall back to the outermost { … } if extra prose slipped in
	if !strings.HasPrefix(s, "{") {
		if a, b := strings.Index(s, "{"), strings.LastIndex(s, "}"); a >= 0 && b > a {
			s = s[a : b+1]
		}
	}
	return s
}
