package manager

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/protonspy/ralph-loop/internal/gate"
	"github.com/protonspy/ralph-loop/internal/graph"
	"github.com/protonspy/ralph-loop/internal/program"
	"github.com/protonspy/ralph-loop/internal/tool"
)

// TestPipelineEndToEnd drives manager.Run through EVERY phase — staff,
// decompose, research, spec-up (author→review→approve, with one review
// rejection to exercise the refine loop), the build inner loop (RED+GREEN,
// git-verified), and E2E — against fake `claude` and `csdd` binaries, proving
// the deterministic body end-to-end without network or tokens.
func TestPipelineEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available for the fake brain")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	installFakeBins(t)
	initGitRepo(t, root)

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		Root:      root,
		Challenge: "say hello to the world",
		Tool:      tool.Runner{Name: "claude"},
		Csdd:      gate.Resolver{"csdd"},
		MaxIter:   6,
		MaxRetry:  2,
		Out:       &out,
	})
	if err != nil {
		t.Fatalf("pipeline failed: %v\n--- log ---\n%s", err, out.String())
	}

	// Program state: the feat went all the way to done.
	p, err := program.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Feats) != 1 || p.Feats[0].ID != "hello-feat" || p.Feats[0].Status != program.StatusDone {
		t.Fatalf("prd.json = %+v", p.Feats)
	}

	// The refine loop ran: the first review rejected the requirements.
	if !strings.Contains(out.String(), "rejected by reviewer") {
		t.Errorf("expected one review rejection in the log:\n%s", out.String())
	}

	// The build loop actually implemented and the tasks are ticked.
	tasks, err := os.ReadFile(filepath.Join(root, "specs/hello-feat/tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tasks), "[ ]") {
		t.Errorf("tasks.md still has open boxes:\n%s", tasks)
	}
	if _, err := os.Stat(filepath.Join(root, "hello.txt")); err != nil {
		t.Error("build iteration did not produce hello.txt")
	}
	// csdd init ran (fake is a no-op) and the CLAUDE.md fallback was seeded.
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Error("CLAUDE.md convention layer was not created")
	}

	// Progress log carries research, staffing and E2E evidence.
	progress, _ := os.ReadFile(program.ProgressPath(root))
	for _, want := range []string{"team staffed", "hello-feat — research", "E2E evidence", "hello-feat — done"} {
		if !strings.Contains(string(progress), want) {
			t.Errorf("progress.md missing %q:\n%s", want, progress)
		}
	}

	// The living documentation graph followed the pipeline.
	store, err := graph.Open(graph.DBPath(root), p.Program)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	facts, err := store.SearchFacts("hello-feat has status done", 5)
	if err != nil || len(facts) != 1 {
		t.Errorf("graph status fact = %+v (err %v)", facts, err)
	}
	// The spec projector recorded the contract...
	if got, _ := store.SearchFacts("spec hello-feat has requirement 1.1", 5); len(got) != 1 {
		t.Errorf("expected a HAS_REQ fact from ProjectSpec, got %+v", got)
	}
	// ...and the build projector recorded what the iteration produced.
	if got, _ := store.SearchFacts("hello.txt implements component Greeter", 5); len(got) != 1 {
		t.Errorf("expected an IMPLEMENTS fact from ProjectBuildUnit, got %+v", got)
	}
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "rl@test"},
		{"config", "user.name", "rl"},
		{"commit", "--allow-empty", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// installFakeBins prepends a fake `claude` (the brain: canned JSON per phase
// marker, file edits + commits in agentic mode) and a fake `csdd` (approve
// flips spec.json, everything validates green) to PATH.
func installFakeBins(t *testing.T) {
	t.Helper()
	bin := t.TempDir()

	writeScript(t, filepath.Join(bin, "claude"), `#!/usr/bin/env bash
set -e
prompt=$(cat)
emit() { jq -n --arg r "$1" '{type:"result",is_error:false,result:$r}'; }

case "$prompt" in
  *"You are the staffer"*)
    emit '{"agents":["agents/e2e-qa"]}' ;;
  *"product architect decomposing"*)
    emit '{"prd_summary":"one hello feat; rest out of scope","feats":[{"id":"hello-feat","title":"Say hello","depends":[]}]}' ;;
  *"You are the researcher"*)
    emit '{"summary":"greetings are well understood","facts_indexed":0}' ;;
  *"adversarial spec reviewer"*)
    if [ ! -f .ralph/.reviewed-once ]; then
      touch .ralph/.reviewed-once
      emit '{"approve":false,"reasons":["acceptance criteria not observable"]}'
    else
      emit '{"approve":true,"reasons":[]}'
    fi ;;
  *"E2E acceptance judge"*)
    emit '{"passed":true,"evidence":"ran hello and saw the greeting","reasons":[]}' ;;
  *"REQUIREMENTS artifact"*)
    mkdir -p specs/hello-feat
    cat > specs/hello-feat/spec.json <<'EOF'
{"feature_name":"hello-feat","phase":"requirements","development_flow":"tdd","approvals":{},"ready_for_implementation":false}
EOF
    echo "WHEN run THEN the system SHALL greet (attempt $RANDOM)" > specs/hello-feat/requirements.md
    git add specs/hello-feat && git commit -q --allow-empty -m "docs(hello-feat): author requirements"
    echo authored ;;
  *"DESIGN artifact"*)
    echo "one Greeter component" > specs/hello-feat/design.md
    git add specs/hello-feat && git commit -q --allow-empty -m "docs(hello-feat): author design"
    echo authored ;;
  *"TASKS artifact"*)
    cat > specs/hello-feat/tasks.md <<'EOF'
- [ ] 1 RED failing greeting test _Requirements: 1.1_ _Boundary: Greeter_
- [ ] 2 GREEN implement greeting _Requirements: 1.1_ _Boundary: Greeter_
EOF
    git add specs/hello-feat && git commit -q --allow-empty -m "docs(hello-feat): author tasks"
    echo authored ;;
  *"spec-driven implementation loop"*)
    sed -i 's/- \[ \]/- [x]/' specs/hello-feat/tasks.md
    echo "hello world" > hello.txt
    git add -A && git commit -q --allow-empty -m "feat(hello-feat): implement greeting"
    echo implemented ;;
  *)
    echo "fake claude: unrecognized prompt" >&2; exit 1 ;;
esac
`)

	writeScript(t, filepath.Join(bin, "csdd"), `#!/usr/bin/env bash
set -e
sub="$1 $2"; feat=$3
rootdir="."; phase=""
while [ $# -gt 0 ]; do
  case "$1" in
    --root) rootdir=$2; shift ;;
    --phase) phase=$2; shift ;;
  esac
  shift
done
cd "$rootdir"
case "$sub" in
  copy*) : ;;
  "spec approve")
    if [ "$phase" = "tasks" ]; then
      spec="specs/$feat/spec.json"
      jq '.ready_for_implementation=true' "$spec" > "$spec.tmp" && mv "$spec.tmp" "$spec"
      git add "$spec" && git commit -q --allow-empty -m "chore($feat): approve tasks"
    fi ;;
  "spec validate"|"spec status"|"spec init"|"spec generate") : ;;
  *) echo "fake csdd: unknown command $*" >&2; exit 1 ;;
esac
`)

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
