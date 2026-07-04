// Command rl (ralph-loop) drives an autonomous, spec-driven implementation loop
// over a csdd workspace: it applies Ralph's stateless-loop methodology (fresh
// context per iteration, file-based state, atomic quality gate) where the unit
// of work is a csdd task and the gate is the csdd contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/protonspy/ralph-loop/internal/gate"
	"github.com/protonspy/ralph-loop/internal/graph"
	"github.com/protonspy/ralph-loop/internal/loop"
	"github.com/protonspy/ralph-loop/internal/manager"
	"github.com/protonspy/ralph-loop/internal/mcp"
	"github.com/protonspy/ralph-loop/internal/program"
	"github.com/protonspy/ralph-loop/internal/spec"
	"github.com/protonspy/ralph-loop/internal/tool"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "plan":
		os.Exit(cmdPlan(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "graph":
		os.Exit(cmdGraph(os.Args[2:]))
	case "graph-mcp":
		os.Exit(cmdGraph(append([]string{"mcp"}, os.Args[2:]...)))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `rl — ralph-loop driver

USAGE
  rl run "<challenge>" [flags]   Manage a challenge end-to-end: staff → decompose →
                                 (per feat) research → spec → build → E2E. No human in the loop.
  rl run <spec-dir>    [flags]   Debug the INNER loop over a single already-approved csdd spec.
  rl plan <spec-dir>             Load a csdd spec and show its next actionable task.
  rl graph export [flags]        Render the living documentation graph (Mermaid/DOT).
                                 Re-projects .ralph/prd.json into .ralph/graph.db first.
  rl graph mcp    [flags]        Serve the graph KB to agents over stdio (MCP).

RUN FLAGS
  --max N          Inner-loop max iterations per feat (default 20).
  --retry N        Consecutive gate failures on one unit before stopping (default 3).
  --tool NAME      AI tool: claude (default) or amp.
  --csdd CMD       How to invoke csdd (default "csdd"; e.g. "npx -y @protonspy/csdd").
  --root PATH      Workspace root (default: cwd for a challenge; inferred for a spec-dir).
  --dry-run        Print the end-to-end plan; mutate nothing, spawn nothing.`)
}

// cmdRun dispatches: a positional that is an existing csdd spec directory drives
// the inner loop (debugging one feat); anything else is a natural-language
// challenge that drives the full manager pipeline.
func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	max := fs.Int("max", 20, "inner-loop max iterations per feat")
	retry := fs.Int("retry", 3, "consecutive gate failures on one unit before stopping")
	toolName := fs.String("tool", "claude", "AI tool: claude|amp")
	csddCmd := fs.String("csdd", "csdd", "how to invoke csdd")
	root := fs.String("root", "", "workspace root")
	dryRun := fs.Bool("dry-run", false, "plan only; spawn nothing")

	// Parse flags that may appear before OR after positionals.
	var positionals []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positionals = append(positionals, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, `usage: rl run "<challenge>" | rl run <spec-dir>`)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tl := tool.Runner{Name: *toolName}
	csdd := gate.Resolver(strings.Fields(*csddCmd))

	var err error
	if isSpecDir(positionals[0]) {
		// INNER loop over one approved spec (debug path).
		specDir := positionals[0]
		wsRoot := *root
		if wsRoot == "" {
			wsRoot = inferRoot(specDir)
		}
		err = loop.Run(ctx, loop.Options{
			Root: wsRoot, SpecDir: specDir, Tool: tl, Csdd: csdd,
			Max: *max, MaxRetry: *retry, DryRun: *dryRun, Out: os.Stdout,
		})
	} else {
		// FULL pipeline from a natural-language challenge.
		challenge := strings.Join(positionals, " ")
		wsRoot := *root
		if wsRoot == "" {
			wsRoot = "."
		}
		if abs, e := filepath.Abs(wsRoot); e == nil {
			wsRoot = abs
		}
		err = manager.Run(ctx, manager.Options{
			Root: wsRoot, Challenge: challenge, Tool: tl, Csdd: csdd,
			MaxIter: *max, MaxRetry: *retry, DryRun: *dryRun, Out: os.Stdout,
		})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// isSpecDir reports whether p is an existing directory containing a spec.json.
func isSpecDir(p string) bool {
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return false
	}
	_, err := os.Stat(filepath.Join(p, "spec.json"))
	return err == nil
}

// inferRoot walks up from a spec dir (specs/<feature>) to the workspace root
// that holds .claude/. Falls back to the spec dir's grandparent.
func inferRoot(specDir string) string {
	abs, err := filepath.Abs(specDir)
	if err != nil {
		return "."
	}
	dir := abs
	for range 40 {
		if st, err := os.Stat(filepath.Join(dir, ".claude")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// specs/<feature> -> workspace root is two levels up
	return filepath.Dir(filepath.Dir(abs))
}

// cmdGraph manages the living documentation graph.
//
//	rl graph export  — refresh the projection from .ralph/prd.json and render it
//	rl graph mcp     — serve the graph to the brain over stdio (MCP)
func cmdGraph(args []string) int {
	if len(args) < 1 || (args[0] != "export" && args[0] != "mcp") {
		fmt.Fprintln(os.Stderr, "usage: rl graph export [--root PATH] [--format mermaid|dot]\n       rl graph mcp    [--root PATH] [--group NAME]")
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("graph "+sub, flag.ContinueOnError)
	root := fs.String("root", ".", "workspace root")
	format := fs.String("format", "mermaid", "output format: mermaid|dot")
	groupFlag := fs.String("group", "", "graph partition (default: the program slug)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	group := *groupFlag
	var prd *program.PRD
	if program.Exists(*root) {
		p, err := program.Load(*root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		prd = p
		if group == "" {
			group = p.Program
		}
	}
	if group == "" {
		group = "default"
	}
	store, err := graph.Open(graph.DBPath(*root), group)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open graph: %v\n", err)
		return 1
	}
	defer store.Close()

	switch sub {
	case "mcp":
		if err := mcp.NewServer(store).Serve(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: mcp serve: %v\n", err)
			return 1
		}
		return 0
	default: // export
		if prd != nil {
			if err := graph.ProjectPRD(store, prd); err != nil {
				fmt.Fprintf(os.Stderr, "error: project prd: %v\n", err)
				return 1
			}
		}
		out, err := graph.Export(store, *format)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Print(out)
		return 0
	}
}

// cmdPlan loads a spec and prints its readiness, task tree, and the next
// actionable leaf task — the task the loop would pick up.
func cmdPlan(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rl plan <spec-dir>")
		return 2
	}
	dir := args[0]
	s, err := spec.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Printf("feature: %s\n", s.Name)
	fmt.Printf("phase:   %s\n", s.JSON.Phase)
	fmt.Printf("flow:    %s\n", s.JSON.DevelopmentFlow)
	fmt.Printf("ready:   %v\n", s.Ready())
	fmt.Println("tasks:")
	for _, t := range s.Tasks {
		printTask(t, 1)
	}

	next, allDone := s.NextActionable()
	fmt.Println()
	switch {
	case allDone:
		fmt.Println("✓ all tasks complete")
	case next == nil:
		fmt.Println("✗ no actionable task (remaining tasks are blocked by unmet dependencies)")
	default:
		fmt.Printf("→ next actionable: %s  %s\n", next.ID, next.Title)
		if next.Boundary != "" {
			fmt.Printf("  boundary: %s\n", next.Boundary)
		}
		if len(next.Requirements) > 0 {
			fmt.Printf("  requirements: %v\n", next.Requirements)
		}
		if len(next.Depends) > 0 {
			fmt.Printf("  depends: %v\n", next.Depends)
		}
	}
	return 0
}

func printTask(t *spec.Task, depth int) {
	box := "[ ]"
	if t.Done {
		box = "[x]"
	}
	tags := ""
	if t.Parallel {
		tags += " (P)"
	}
	if t.Boundary != "" {
		tags += " {" + t.Boundary + "}"
	}
	fmt.Printf("%s%s %s %s%s\n", indent(depth), box, t.ID, t.Title, tags)
	for _, c := range t.Children {
		printTask(c, depth+1)
	}
}

func indent(n int) string {
	return strings.Repeat("  ", n)
}
