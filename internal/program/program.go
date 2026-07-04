// Package program is ralph-loop's OWN state layer — the altitude above csdd.
// It tracks a challenge decomposed into feats (each feat = one csdd spec) in
// .ralph/prd.json, plus a .ralph/progress.md learnings log. This is Ralph's
// prd.json/progress.txt idea moved up one level: the tracker's item is a whole
// feature, whose implementation is delegated to the csdd contract.
package program

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Status is a feat's lifecycle stage.
type Status string

const (
	StatusPlanned       Status = "planned"        // decomposed, no spec yet
	StatusSpecGenerated Status = "spec_generated" // csdd artifacts authored, not approved
	StatusApproved      Status = "approved"       // ready_for_implementation
	StatusImplementing  Status = "implementing"   // inner build loop running
	StatusDone          Status = "done"           // built AND E2E-accepted
)

// Feat is one unit of the program: a feature that maps 1:1 to a csdd spec.
type Feat struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Spec    string   `json:"spec"`    // specs/<id>, relative to root
	Depends []string `json:"depends"` // feat IDs that must be done first
	Status  Status   `json:"status"`
}

// PRD is the whole program: the challenge and its feats.
type PRD struct {
	Program   string  `json:"program"`
	Challenge string  `json:"challenge"`
	PRDPath   string  `json:"prd,omitempty"` // path to the full PRD markdown, once written
	Branch    string  `json:"branch"`
	Feats     []*Feat `json:"feats"`
}

// Dir returns the .ralph directory for a workspace root.
func Dir(root string) string { return filepath.Join(root, ".ralph") }

// Path returns the .ralph/prd.json path.
func Path(root string) string { return filepath.Join(Dir(root), "prd.json") }

// ProgressPath returns the .ralph/progress.md path.
func ProgressPath(root string) string { return filepath.Join(Dir(root), "progress.md") }

// Exists reports whether a program has been bootstrapped at root.
func Exists(root string) bool {
	_, err := os.Stat(Path(root))
	return err == nil
}

// Load reads .ralph/prd.json.
func Load(root string) (*PRD, error) {
	raw, err := os.ReadFile(Path(root))
	if err != nil {
		return nil, err
	}
	var p PRD
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(root), err)
	}
	return &p, nil
}

// Save writes .ralph/prd.json atomically-ish (write temp, rename).
func Save(root string, p *PRD) error {
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(root) + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(root))
}

// Bootstrap creates .ralph/ and an initial prd.json for a challenge, with no
// feats yet (decomposition fills them). It is a no-op-safe: if a program already
// exists it is loaded and returned unchanged.
func Bootstrap(root, challenge string) (*PRD, error) {
	if Exists(root) {
		return Load(root)
	}
	slug := Slug(challenge)
	p := &PRD{
		Program:   slug,
		Challenge: challenge,
		Branch:    "ralph/" + slug,
		Feats:     []*Feat{},
	}
	if err := Save(root, p); err != nil {
		return nil, err
	}
	header := fmt.Sprintf("# Ralph-Loop Progress — %s\n\nChallenge: %s\n\n---\n", slug, challenge)
	if err := os.WriteFile(ProgressPath(root), []byte(header), 0o644); err != nil {
		return nil, err
	}
	// .ralph is runtime state (prd, progress, graph.db, mcp configs) — make the
	// directory self-ignoring so it never dirties the workspace's git status,
	// which the build gate checks after every iteration.
	if err := os.WriteFile(filepath.Join(Dir(root), ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return nil, err
	}
	return p, nil
}

// NextFeat returns the first not-done feat whose dependencies are all done.
// The bool reports whether the whole program is complete.
func (p *PRD) NextFeat() (*Feat, bool) {
	done := map[string]bool{}
	for _, f := range p.Feats {
		done[f.ID] = f.Status == StatusDone
	}
	allDone := true
	for _, f := range p.Feats {
		if f.Status == StatusDone {
			continue
		}
		allDone = false
		if depsMet(f, done) {
			return f, false
		}
	}
	return nil, allDone
}

func depsMet(f *Feat, done map[string]bool) bool {
	for _, d := range f.Depends {
		if !done[d] {
			return false
		}
	}
	return true
}

// Find returns the feat with the given ID, or nil.
func (p *PRD) Find(id string) *Feat {
	for _, f := range p.Feats {
		if f.ID == id {
			return f
		}
	}
	return nil
}

// AppendProgress adds a section to .ralph/progress.md. Append-only, like Ralph.
func AppendProgress(root, heading, body string) error {
	f, err := os.OpenFile(ProgressPath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n## %s\n\n%s\n", heading, strings.TrimSpace(body))
	return err
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a free-text challenge into a kebab-case program/branch name,
// matching csdd's KebabCheck (^[a-z][a-z0-9]*(-[a-z0-9]+)*$).
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "program"
	}
	// keep it short-ish
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	// must start with a letter
	if s[0] < 'a' || s[0] > 'z' {
		s = "p-" + s
	}
	return s
}
