// Package gitx provides the minimal git operations the loop needs to enforce
// atomic, all-or-nothing iterations: snapshot the HEAD before an iteration and,
// if the gate fails after the AI has committed, hard-reset back to it — so a
// failed behavior leaves the repository exactly as it was.
package gitx

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Head returns the current commit SHA.
func Head(ctx context.Context, dir string) (string, error) {
	return git(ctx, dir, "rev-parse", "HEAD")
}

// ResetHard resets the working tree and index to ref, discarding the AI's
// commit and any uncommitted changes from a failed iteration.
func ResetHard(ctx context.Context, dir, ref string) error {
	_, err := git(ctx, dir, "reset", "--hard", ref)
	if err != nil {
		return err
	}
	// drop untracked files the failed iteration may have created
	_, err = git(ctx, dir, "clean", "-fd")
	return err
}

// HeadMoved reports whether HEAD differs from ref (i.e. the AI committed).
func HeadMoved(ctx context.Context, dir, ref string) (bool, error) {
	cur, err := Head(ctx, dir)
	if err != nil {
		return false, err
	}
	return cur != ref, nil
}

// Dirty reports whether the working tree has uncommitted changes.
func Dirty(ctx context.Context, dir string) (bool, error) {
	out, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// CurrentBranch returns the checked-out branch name, or "" on a detached HEAD.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" {
		return "", nil // detached
	}
	return out, nil
}

// EnsureBranch checks out branch, creating it (forked from the current HEAD) if
// it does not exist. A no-op when already on it. This is what isolates a
// program's autonomous work onto ralph/<slug> instead of polluting the base
// branch.
func EnsureBranch(ctx context.Context, dir, branch string) error {
	cur, err := CurrentBranch(ctx, dir)
	if err != nil {
		return err
	}
	if cur == branch {
		return nil
	}
	if _, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err := git(ctx, dir, "checkout", branch)
		return err
	}
	_, err = git(ctx, dir, "checkout", "-b", branch)
	return err
}

// CommitAll stages every change (untracked included) and commits it. It is a
// no-op on a clean tree. Used to establish a clean baseline before the atomic
// build gate, so front-of-pipeline artifacts (the staffed team under .claude/,
// the generated CLAUDE.md) don't count as a "dirty" iteration.
func CommitAll(ctx context.Context, dir, msg string) error {
	dirty, err := Dirty(ctx, dir)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if _, err := git(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	_, err = git(ctx, dir, "commit", "-m", msg)
	return err
}
