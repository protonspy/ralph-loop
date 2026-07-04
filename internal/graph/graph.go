// Package graph is ralph-loop's local temporal knowledge graph — a lightweight,
// embedded (pure-Go SQLite, no Docker, no service) reimplementation of the model
// proven by Graphiti (getzep/graphiti). It is ralph-loop's SINGLE knowledge base
// — entities, bi-temporal facts, and raw episodes/documents — replacing any
// external KB/memory service (context-mode is not used).
//
// Design mirror of Graphiti, adapted: in Graphiti an LLM extracts entities/edges
// from raw episodes. In ralph-loop the BRAIN (Claude) is that LLM — so this store
// is purely mechanical. It persists brain-provided facts, dedupes by normalized
// text, and (the differentiating feature) resolves contradictions temporally:
// nothing is ever deleted — a superseded fact gets invalid_at + expired_at set
// and simply leaves the "current" view.
//
// Bi-temporal model (Graphiti's four dates):
//
//	valid_at   — when the fact BECAME true in the real world
//	invalid_at — when it CEASED to be true in the real world (null = still true)
//	created_at — when we first wrote the fact (system time, immutable)
//	expired_at — when the SYSTEM recorded it as superseded (soft-delete marker)
//
// A fact is CURRENT iff expired_at IS NULL.
package graph

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is a per-project graph, backed by a single SQLite file (e.g. .ralph/graph.db).
type Store struct {
	db    *sql.DB
	group string // partition key (group_id); one program per store by default
	now   func() time.Time
}

// Open opens (creating if needed) the graph database and ensures the schema.
// The parent directory (e.g. .ralph/) is created if missing so a freshly-spawned
// `rl graph mcp --root <dir>` works before the pipeline has scaffolded it.
func Open(path, group string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if group == "" {
		group = "default"
	}
	s := &Store{db: db, group: group, now: func() time.Time { return time.Now().UTC() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS entity_nodes(
  uuid TEXT PRIMARY KEY, group_id TEXT NOT NULL, kind TEXT NOT NULL,
  name TEXT NOT NULL, norm_name TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '', attrs TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS ux_entity ON entity_nodes(group_id, kind, norm_name);
CREATE INDEX IF NOT EXISTS ix_entity_norm ON entity_nodes(norm_name);

CREATE TABLE IF NOT EXISTS episodic_nodes(
  uuid TEXT PRIMARY KEY, group_id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'text', content TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, valid_at TEXT);
CREATE INDEX IF NOT EXISTS ix_episode_valid ON episodic_nodes(valid_at);

CREATE TABLE IF NOT EXISTS entity_edges(
  uuid TEXT PRIMARY KEY, group_id TEXT NOT NULL,
  src TEXT NOT NULL, dst TEXT NOT NULL, rel TEXT NOT NULL,
  fact TEXT NOT NULL, norm_fact TEXT NOT NULL,
  episodes TEXT NOT NULL DEFAULT '[]', attrs TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL, valid_at TEXT, invalid_at TEXT, expired_at TEXT);
CREATE INDEX IF NOT EXISTS ix_edge_src ON entity_edges(src);
CREATE INDEX IF NOT EXISTS ix_edge_dst ON entity_edges(dst);
CREATE INDEX IF NOT EXISTS ix_edge_srcrel ON entity_edges(src, rel);
CREATE INDEX IF NOT EXISTS ix_edge_expired ON entity_edges(expired_at);
CREATE INDEX IF NOT EXISTS ix_edge_valid ON entity_edges(valid_at);
CREATE INDEX IF NOT EXISTS ix_edge_invalid ON entity_edges(invalid_at);

-- Full-text search (FTS5, default unicode61 tokenizer). Optimized for word/prose
-- recall — terms match across hyphen/dot/slash boundaries ("collision" finds
-- "collision-system"; "lifecycle" finds "lifecycle."). Exact ID/path distinction
-- (1.1 vs 1.2) is intentionally NOT a search concern — use structured lookups for
-- that. Each FTS table shares the base table's rowid and is synced by triggers.
CREATE VIRTUAL TABLE IF NOT EXISTS entity_fts USING fts5(name, summary);
CREATE TRIGGER IF NOT EXISTS entity_fts_ai AFTER INSERT ON entity_nodes BEGIN
  INSERT INTO entity_fts(rowid, name, summary) VALUES(new.rowid, new.name, new.summary);
END;
CREATE TRIGGER IF NOT EXISTS entity_fts_au AFTER UPDATE ON entity_nodes BEGIN
  UPDATE entity_fts SET name=new.name, summary=new.summary WHERE rowid=new.rowid;
END;
CREATE TRIGGER IF NOT EXISTS entity_fts_ad AFTER DELETE ON entity_nodes BEGIN
  DELETE FROM entity_fts WHERE rowid=old.rowid;
END;

CREATE VIRTUAL TABLE IF NOT EXISTS edge_fts USING fts5(fact, rel);
CREATE TRIGGER IF NOT EXISTS edge_fts_ai AFTER INSERT ON entity_edges BEGIN
  INSERT INTO edge_fts(rowid, fact, rel) VALUES(new.rowid, new.fact, new.rel);
END;
CREATE TRIGGER IF NOT EXISTS edge_fts_au AFTER UPDATE ON entity_edges BEGIN
  UPDATE edge_fts SET fact=new.fact, rel=new.rel WHERE rowid=new.rowid;
END;
CREATE TRIGGER IF NOT EXISTS edge_fts_ad AFTER DELETE ON entity_edges BEGIN
  DELETE FROM edge_fts WHERE rowid=old.rowid;
END;

CREATE VIRTUAL TABLE IF NOT EXISTS episodic_fts USING fts5(name, content);
CREATE TRIGGER IF NOT EXISTS episodic_fts_ai AFTER INSERT ON episodic_nodes BEGIN
  INSERT INTO episodic_fts(rowid, name, content) VALUES(new.rowid, new.name, new.content);
END;`
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}
	return s.backfillFTS()
}

// backfillFTS populates an FTS index that exists but is empty — the upgrade path
// for a graph.db created before FTS was added (triggers only cover new writes).
// A no-op once the index is populated, and on a fresh empty database.
func (s *Store) backfillFTS() error {
	for _, bf := range []struct{ fts, stmt string }{
		{"entity_fts", `INSERT INTO entity_fts(rowid, name, summary) SELECT rowid, name, summary FROM entity_nodes`},
		{"edge_fts", `INSERT INTO edge_fts(rowid, fact, rel) SELECT rowid, fact, rel FROM entity_edges`},
		{"episodic_fts", `INSERT INTO episodic_fts(rowid, name, content) SELECT rowid, name, content FROM episodic_nodes`},
	} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + bf.fts).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := s.db.Exec(bf.stmt); err != nil {
			return err
		}
	}
	return nil
}

// ---- models ----

// Entity is a semantic node (a person, module, decision, feat, …).
type Entity struct {
	UUID, Kind, Name, Summary string
	CreatedAt                 time.Time
}

// Fact is a bi-temporal entity edge: src —rel→ dst, described by Fact.
type Fact struct {
	UUID, Src, Dst, Rel, Fact     string
	Episodes                      []string
	CreatedAt                     time.Time
	ValidAt, InvalidAt, ExpiredAt *time.Time
}

// Current reports whether the fact is in the active view (not superseded).
func (f Fact) Current() bool { return f.ExpiredAt == nil }

// Episode is a raw observation/document (the provenance tier): a research
// finding, a doc excerpt, a decision's context.
type Episode struct {
	UUID, Name, Source, Content string
	CreatedAt                   time.Time
	ValidAt                     *time.Time
}

// ---- entities ----

// UpsertEntity returns the canonical entity for (kind, name), creating it on
// first sight. Dedup is by normalized name within (group, kind) — Graphiti's
// Tier-1 exact-match resolution.
func (s *Store) UpsertEntity(kind, name string) (Entity, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return Entity{}, fmt.Errorf("kind and name are required")
	}
	norm := normalize(name)
	now := s.now()
	uuid := newUUID()
	_, err := s.db.Exec(
		`INSERT INTO entity_nodes(uuid, group_id, kind, name, norm_name, created_at)
		 VALUES(?,?,?,?,?,?)
		 ON CONFLICT(group_id, kind, norm_name) DO NOTHING`,
		uuid, s.group, kind, name, norm, rfc(now))
	if err != nil {
		return Entity{}, err
	}
	var e Entity
	var created string
	err = s.db.QueryRow(
		`SELECT uuid, kind, name, summary, created_at FROM entity_nodes
		 WHERE group_id=? AND kind=? AND norm_name=?`, s.group, kind, norm).
		Scan(&e.UUID, &e.Kind, &e.Name, &e.Summary, &created)
	if err != nil {
		return Entity{}, err
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return e, nil
}

// ---- episodes ----

// AddEpisode records a raw observation (the provenance/source tier). validAt is
// the real-world time the observation refers to (zero ⇒ now).
func (s *Store) AddEpisode(name, source, content string, validAt time.Time) (string, error) {
	uuid := newUUID()
	now := s.now()
	if validAt.IsZero() {
		validAt = now
	}
	_, err := s.db.Exec(
		`INSERT INTO episodic_nodes(uuid, group_id, name, source, content, created_at, valid_at)
		 VALUES(?,?,?,?,?,?,?)`,
		uuid, s.group, name, orDefault(source, "text"), content, rfc(now), rfc(validAt))
	return uuid, err
}

// ---- facts (edges) ----

// FactInput describes a new fact to add.
type FactInput struct {
	Src, Dst, Rel, Fact string
	ValidAt             time.Time // real-world start (zero ⇒ now)
	Episode             string    // optional provenance episode uuid
	Supersedes          []string  // edge uuids this fact contradicts (brain-decided)
}

// AddFact persists a fact. If an identical CURRENT fact exists (same src/dst/rel
// and normalized text) it is reused with the new episode appended (edge dedup).
// Any Supersedes edges are temporally invalidated (never deleted).
func (s *Store) AddFact(in FactInput) (Fact, error) {
	if in.Src == "" || in.Dst == "" || in.Rel == "" || strings.TrimSpace(in.Fact) == "" {
		return Fact{}, fmt.Errorf("src, dst, rel and fact are required")
	}
	now := s.now()
	validAt := in.ValidAt
	if validAt.IsZero() {
		validAt = now
	}
	normFact := normalize(in.Fact)

	// Edge dedup fast-path: reuse a current, identical edge.
	if existing, ok, err := s.findCurrentEdge(in.Src, in.Dst, in.Rel, normFact); err != nil {
		return Fact{}, err
	} else if ok {
		if in.Episode != "" {
			existing.Episodes = appendUnique(existing.Episodes, in.Episode)
			eps, _ := json.Marshal(existing.Episodes)
			if _, err := s.db.Exec(`UPDATE entity_edges SET episodes=? WHERE uuid=?`, string(eps), existing.UUID); err != nil {
				return Fact{}, err
			}
		}
		return existing, nil
	}

	// Temporal invalidation of superseded facts: set invalid_at (valid-time end)
	// and expired_at (system time of the operation). History is preserved.
	for _, uuid := range in.Supersedes {
		if err := s.invalidate(uuid, validAt, now); err != nil {
			return Fact{}, err
		}
	}

	uuid := newUUID()
	var eps []string
	if in.Episode != "" {
		eps = []string{in.Episode}
	}
	epsJSON, _ := json.Marshal(eps)
	_, err := s.db.Exec(
		`INSERT INTO entity_edges(uuid, group_id, src, dst, rel, fact, norm_fact, episodes, created_at, valid_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		uuid, s.group, in.Src, in.Dst, in.Rel, in.Fact, normFact, string(epsJSON), rfc(now), rfc(validAt))
	if err != nil {
		return Fact{}, err
	}
	return s.factByUUID(uuid)
}

// InvalidateFact marks a fact as no longer true as of invalidAt (real-world),
// recording expired_at = now (system time). The row is kept for history.
func (s *Store) InvalidateFact(uuid string, invalidAt time.Time) error {
	now := s.now()
	if invalidAt.IsZero() {
		invalidAt = now
	}
	return s.invalidate(uuid, invalidAt, now)
}

func (s *Store) invalidate(uuid string, invalidAt, expiredAt time.Time) error {
	res, err := s.db.Exec(
		`UPDATE entity_edges SET invalid_at=?, expired_at=?
		 WHERE uuid=? AND group_id=? AND expired_at IS NULL`,
		rfc(invalidAt), rfc(expiredAt), uuid, s.group)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no current fact with uuid %q to invalidate", uuid)
	}
	return nil
}

// ---- queries ----

// SearchNodes finds entities whose name or summary matches the query via
// full-text search (FTS5), ranked by relevance. A blank query lists the most
// recent entities.
func (s *Store) SearchNodes(query string, limit int) ([]Entity, error) {
	limit = clampLimit(limit)
	m := ftsMatch(query)
	if m == "" {
		return s.scanEntities(
			`SELECT uuid, kind, name, summary, created_at FROM entity_nodes
			 WHERE group_id=? ORDER BY created_at DESC LIMIT ?`, s.group, limit)
	}
	return s.scanEntities(
		`SELECT e.uuid, e.kind, e.name, e.summary, e.created_at
		 FROM entity_fts JOIN entity_nodes e ON e.rowid = entity_fts.rowid
		 WHERE e.group_id=? AND entity_fts MATCH ?
		 ORDER BY entity_fts.rank LIMIT ?`, s.group, m, limit)
}

func (s *Store) scanEntities(q string, args ...any) ([]Entity, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entity
	for rows.Next() {
		var e Entity
		var created string
		if err := rows.Scan(&e.UUID, &e.Kind, &e.Name, &e.Summary, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindEntity looks up the canonical entity for (kind, name) without creating it.
func (s *Store) FindEntity(kind, name string) (Entity, bool, error) {
	var e Entity
	var created string
	err := s.db.QueryRow(
		`SELECT uuid, kind, name, summary, created_at FROM entity_nodes
		 WHERE group_id=? AND kind=? AND norm_name=?`, s.group, kind, normalize(name)).
		Scan(&e.UUID, &e.Kind, &e.Name, &e.Summary, &created)
	if err == sql.ErrNoRows {
		return Entity{}, false, nil
	}
	if err != nil {
		return Entity{}, false, err
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return e, true, nil
}

// Entities returns every entity in the group (for export/projection).
func (s *Store) Entities() ([]Entity, error) {
	rows, err := s.db.Query(
		`SELECT uuid, kind, name, summary, created_at FROM entity_nodes
		 WHERE group_id=? ORDER BY kind, norm_name`, s.group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entity
	for rows.Next() {
		var e Entity
		var created string
		if err := rows.Scan(&e.UUID, &e.Kind, &e.Name, &e.Summary, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CurrentFacts returns every current (non-superseded) fact in the group.
func (s *Store) CurrentFacts() ([]Fact, error) {
	return s.queryFacts(
		`SELECT `+edgeCols+` FROM entity_edges
		 WHERE group_id=? AND expired_at IS NULL ORDER BY created_at`, s.group)
}

// SearchFacts finds CURRENT (non-superseded) facts whose text or relation
// matches the query via full-text search (FTS5), ranked by relevance. A blank
// query lists the most recent current facts.
func (s *Store) SearchFacts(query string, limit int) ([]Fact, error) {
	limit = clampLimit(limit)
	m := ftsMatch(query)
	if m == "" {
		return s.queryFacts(
			`SELECT `+edgeCols+` FROM entity_edges
			 WHERE group_id=? AND expired_at IS NULL
			 ORDER BY created_at DESC LIMIT ?`, s.group, limit)
	}
	return s.queryFacts(
		`SELECT `+edgeColsE+` FROM edge_fts JOIN entity_edges e ON e.rowid = edge_fts.rowid
		 WHERE e.group_id=? AND e.expired_at IS NULL AND edge_fts MATCH ?
		 ORDER BY edge_fts.rank LIMIT ?`, s.group, m, limit)
}

// SearchEpisodes finds raw observations/documents (research findings, doc
// excerpts) whose name or content matches the query via full-text search — the
// content-recall path the FTS index unlocks. A blank query lists recent episodes.
func (s *Store) SearchEpisodes(query string, limit int) ([]Episode, error) {
	limit = clampLimit(limit)
	m := ftsMatch(query)
	var rows *sql.Rows
	var err error
	if m == "" {
		rows, err = s.db.Query(
			`SELECT uuid, name, source, content, created_at, valid_at FROM episodic_nodes
			 WHERE group_id=? ORDER BY created_at DESC LIMIT ?`, s.group, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT ep.uuid, ep.name, ep.source, ep.content, ep.created_at, ep.valid_at
			 FROM episodic_fts JOIN episodic_nodes ep ON ep.rowid = episodic_fts.rowid
			 WHERE ep.group_id=? AND episodic_fts MATCH ?
			 ORDER BY episodic_fts.rank LIMIT ?`, s.group, m, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		var created string
		var valid sql.NullString
		if err := rows.Scan(&e.UUID, &e.Name, &e.Source, &e.Content, &created, &valid); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, created)
		e.ValidAt = parseNullTime(valid)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ftsMatch turns free text into a safe FTS5 MATCH expression: each
// whitespace-separated term becomes a double-quoted literal, implicitly AND-ed.
// Quoting neutralizes FTS operators (AND/OR/NOT/NEAR/*) a caller might type and,
// with the tokenizer's tokenchars, keeps hyphen/dot/slash terms whole. Returns
// "" for a blank query so callers can fall back to a recency listing.
func ftsMatch(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

// Neighbors returns current facts reachable from an entity within depth hops
// (breadth-first over the current edge set), following either direction.
func (s *Store) Neighbors(uuid string, depth int) ([]Fact, error) {
	if depth < 1 {
		depth = 1
	}
	q := `
WITH RECURSIVE reach(node, d) AS (
  SELECT ?, 0
  UNION
  SELECT CASE WHEN e.src = r.node THEN e.dst ELSE e.src END, r.d + 1
  FROM reach r
  JOIN entity_edges e
    ON (e.src = r.node OR e.dst = r.node) AND e.group_id = ? AND e.expired_at IS NULL
  WHERE r.d < ?
)
SELECT ` + edgeCols + ` FROM entity_edges e
WHERE e.group_id = ? AND e.expired_at IS NULL
  AND (e.src IN (SELECT node FROM reach) OR e.dst IN (SELECT node FROM reach))
ORDER BY e.created_at DESC`
	return s.queryFacts(q, uuid, s.group, depth, s.group)
}

// FactsAsOf returns facts that were true in the REAL WORLD at time t
// (valid_at <= t AND (invalid_at IS NULL OR invalid_at > t)), regardless of
// whether they were later superseded — the point-in-time (valid-time) view.
func (s *Store) FactsAsOf(t time.Time) ([]Fact, error) {
	ts := rfc(t)
	return s.queryFacts(
		`SELECT `+edgeCols+` FROM entity_edges
		 WHERE group_id=? AND valid_at IS NOT NULL AND valid_at <= ?
		   AND (invalid_at IS NULL OR invalid_at > ?)
		 ORDER BY valid_at DESC`, s.group, ts, ts)
}

// ---- internals ----

const edgeCols = `uuid, src, dst, rel, fact, episodes, created_at, valid_at, invalid_at, expired_at`

// edgeColsE is edgeCols qualified with the "e" alias, for joins against edge_fts
// where the bare fact/rel columns would be ambiguous.
const edgeColsE = `e.uuid, e.src, e.dst, e.rel, e.fact, e.episodes, e.created_at, e.valid_at, e.invalid_at, e.expired_at`

func (s *Store) queryFacts(q string, args ...any) ([]Fact, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) factByUUID(uuid string) (Fact, error) {
	row := s.db.QueryRow(`SELECT `+edgeCols+` FROM entity_edges WHERE uuid=?`, uuid)
	return scanFact(row)
}

func (s *Store) findCurrentEdge(src, dst, rel, normFact string) (Fact, bool, error) {
	row := s.db.QueryRow(
		`SELECT `+edgeCols+` FROM entity_edges
		 WHERE group_id=? AND src=? AND dst=? AND rel=? AND norm_fact=? AND expired_at IS NULL
		 LIMIT 1`, s.group, src, dst, rel, normFact)
	f, err := scanFact(row)
	if err == sql.ErrNoRows {
		return Fact{}, false, nil
	}
	if err != nil {
		return Fact{}, false, err
	}
	return f, true, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFact(r scanner) (Fact, error) {
	var f Fact
	var eps string
	var valid, invalid, expired sql.NullString
	var created string
	if err := r.Scan(&f.UUID, &f.Src, &f.Dst, &f.Rel, &f.Fact, &eps, &created, &valid, &invalid, &expired); err != nil {
		return Fact{}, err
	}
	_ = json.Unmarshal([]byte(eps), &f.Episodes)
	f.CreatedAt, _ = time.Parse(time.RFC3339, created)
	f.ValidAt = parseNullTime(valid)
	f.InvalidAt = parseNullTime(invalid)
	f.ExpiredAt = parseNullTime(expired)
	return f, nil
}

var wsRe = regexp.MustCompile(`\s+`)

func normalize(s string) string {
	return wsRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func appendUnique(xs []string, x string) []string {
	if slices.Contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func clampLimit(n int) int {
	if n <= 0 {
		return 20
	}
	if n > 200 {
		return 200
	}
	return n
}
