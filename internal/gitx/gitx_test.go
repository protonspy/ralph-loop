package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo makes a temp git repo with one seed commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "rl@test"},
		{"config", "user.name", "rl"},
		{"commit", "--allow-empty", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestEnsureBranchCreatesAndReuses(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)

	if err := EnsureBranch(ctx, dir, "ralph/demo"); err != nil {
		t.Fatalf("create: %v", err)
	}
	cur, err := CurrentBranch(ctx, dir)
	if err != nil || cur != "ralph/demo" {
		t.Fatalf("current branch = %q (err %v), want ralph/demo", cur, err)
	}

	// Idempotent: already on it.
	if err := EnsureBranch(ctx, dir, "ralph/demo"); err != nil {
		t.Fatalf("reuse (same branch): %v", err)
	}

	// Switch away and back — the branch already exists, so checkout not -b.
	if _, err := git(ctx, dir, "checkout", "-q", "-b", "other"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBranch(ctx, dir, "ralph/demo"); err != nil {
		t.Fatalf("re-checkout existing: %v", err)
	}
	if cur, _ := CurrentBranch(ctx, dir); cur != "ralph/demo" {
		t.Fatalf("after re-checkout current = %q, want ralph/demo", cur)
	}
}

func TestCommitAllIsNoopWhenClean(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)

	head, _ := Head(ctx, dir)
	if err := CommitAll(ctx, dir, "chore: nothing"); err != nil {
		t.Fatalf("clean commit-all: %v", err)
	}
	if now, _ := Head(ctx, dir); now != head {
		t.Fatalf("CommitAll on a clean tree moved HEAD %s -> %s", head, now)
	}

	// Dirty the tree with an untracked file — CommitAll must stage and commit it.
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(ctx, dir, "chore: conventions"); err != nil {
		t.Fatalf("dirty commit-all: %v", err)
	}
	if now, _ := Head(ctx, dir); now == head {
		t.Fatal("CommitAll did not commit the untracked file")
	}
	if dirty, _ := Dirty(ctx, dir); dirty {
		t.Fatal("tree still dirty after CommitAll")
	}
}
