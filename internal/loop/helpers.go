package loop

import (
	"strings"

	"github.com/protonspy/ralph-loop/internal/tool"
)

func toolName(t tool.Runner) string {
	if t.Name == "" {
		return "claude"
	}
	return t.Name
}

func joinIDs(ids []string) string { return strings.Join(ids, "+") }

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// indentBlock indents every line of s for readable prompt previews in dry-run.
func indentBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    │ " + l
	}
	return strings.Join(lines, "\n")
}
