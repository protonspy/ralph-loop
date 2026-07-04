// Package gate runs the csdd contract as the quality gate for an iteration.
// csdd is the sanctioned enforcer: we shell out to it and interpret its exit
// codes rather than re-implementing validation. This is the "binary-as-oracle"
// integration; it can later be swapped for in-process csdd package calls.
package gate

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ExitContractViolation is csdd's exit code for a mechanical SDD failure — the
// input was well-formed but broke the contract (distinct from a generic error).
const ExitContractViolation = 2

// Resolver locates the csdd command. Typical values:
//
//	[]string{"csdd"}                          // on PATH
//	[]string{"npx", "-y", "@protonspy/csdd"}  // via npx (this env)
type Resolver []string

// Result is the outcome of a gate check.
type Result struct {
	OK         bool   // validation passed (exit 0)
	Violation  bool   // exit 2: contract violation
	ExitCode   int    // raw exit code
	Output     string // combined stdout+stderr
}

// Command renders the resolver as a shell-ish string, for prompts that tell
// the brain how to invoke csdd itself.
func (r Resolver) Command() string { return strings.Join(r, " ") }

// Validate runs `csdd spec validate <feature> --root <root>`.
func (r Resolver) Validate(ctx context.Context, root, feature string) Result {
	return r.run(ctx, "", "spec", "validate", feature, "--root", root)
}

// Status runs `csdd spec status <feature>` (useful for reporting).
func (r Resolver) Status(ctx context.Context, root, feature string) Result {
	return r.run(ctx, "", "spec", "status", feature, "--root", root)
}

// Approve runs `csdd spec approve <feature> --phase <phase> --root <root>`.
// csdd re-validates on approve, so this is an autonomous quality gate: it only
// succeeds when the phase artifact passes the contract.
func (r Resolver) Approve(ctx context.Context, root, feature, phase string) Result {
	return r.run(ctx, "", "spec", "approve", feature, "--phase", phase, "--root", root)
}

// RunIn runs an arbitrary csdd command with the working directory set to dir —
// the factory surface (`csdd agent create`, `csdd skill create`, …), which
// operates on the workspace it is run from rather than taking --root.
func (r Resolver) RunIn(ctx context.Context, dir string, args ...string) Result {
	return r.run(ctx, dir, args...)
}

func (r Resolver) run(ctx context.Context, dir string, args ...string) Result {
	argv := append(append([]string{}, r...), args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	res := Result{Output: buf.String()}
	if err == nil {
		res.OK = true
		return res
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		res.ExitCode = ee.ExitCode()
		res.Violation = res.ExitCode == ExitContractViolation
		return res
	}
	// couldn't even launch csdd
	res.ExitCode = -1
	return res
}
