package program

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"fazer um jogo estilo WoW": "fazer-um-jogo-estilo-wow",
		"  Hello, World!  ":        "hello-world",
		"123 start":                "p-123-start",
		"":                         "program",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBootstrapAndReload(t *testing.T) {
	root := t.TempDir()
	p, err := Bootstrap(root, "fazer um jogo estilo wow")
	if err != nil {
		t.Fatal(err)
	}
	if p.Program != "fazer-um-jogo-estilo-wow" {
		t.Fatalf("program = %q", p.Program)
	}
	if !Exists(root) {
		t.Fatal("Exists should be true after Bootstrap")
	}
	// ralph-loop must NOT scaffold a git branch — that's csdd's job.
	if _, err := os.Stat(filepath.Join(root, ".claude")); err != nil {
		t.Errorf(".claude anchor should be scaffolded: %v", err)
	}
	// bootstrap is idempotent
	p2, err := Bootstrap(root, "different challenge")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Challenge != p.Challenge {
		t.Fatalf("bootstrap not idempotent: %q", p2.Challenge)
	}

	// round-trip feats
	p.Feats = []*Feat{
		{ID: "a", Spec: filepath.Join("specs", "a"), Status: StatusPlanned},
		{ID: "b", Depends: []string{"a"}, Status: StatusPlanned},
	}
	if err := Save(root, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Feats) != 2 || got.Feats[1].Depends[0] != "a" {
		t.Fatalf("round-trip failed: %+v", got.Feats)
	}
}

func TestNextFeatRespectsDeps(t *testing.T) {
	p := &PRD{Feats: []*Feat{
		{ID: "a", Status: StatusPlanned},
		{ID: "b", Depends: []string{"a"}, Status: StatusPlanned},
		{ID: "c", Depends: []string{"b"}, Status: StatusPlanned},
	}}

	if f, _ := p.NextFeat(); f == nil || f.ID != "a" {
		t.Fatalf("want a, got %v", featID(f))
	}
	p.Find("a").Status = StatusDone
	if f, _ := p.NextFeat(); f == nil || f.ID != "b" {
		t.Fatalf("after a done, want b, got %v", featID(f))
	}
	p.Find("b").Status = StatusDone
	if f, _ := p.NextFeat(); f == nil || f.ID != "c" {
		t.Fatalf("after b done, want c, got %v", featID(f))
	}
	p.Find("c").Status = StatusDone
	f, allDone := p.NextFeat()
	if f != nil || !allDone {
		t.Fatalf("want complete, got %v allDone=%v", featID(f), allDone)
	}
}

func TestAppendProgress(t *testing.T) {
	root := t.TempDir()
	if _, err := Bootstrap(root, "x"); err != nil {
		t.Fatal(err)
	}
	if err := AppendProgress(root, "feat-a — done", "learned something"); err != nil {
		t.Fatal(err)
	}
}

func featID(f *Feat) string {
	if f == nil {
		return "<nil>"
	}
	return f.ID
}
