# ralph-loop (`rl`)

An autonomous, spec-driven software-delivery **manager**. Given one natural-language
challenge, `rl` staffs a bespoke AI team, decomposes the challenge into vertical-slice
feats, manufactures a [csdd](https://github.com/protonspy/csdd) contract for each,
builds it with fresh-context RED/GREEN iterations behind atomic git gates, and only
marks a feat done after an E2E acceptance pass. **No human in the loop** — the human
receives the delivered result.

It fuses the Ralph methodology (stateless loop, fresh context per iteration,
file-based state), the csdd contract (EARS requirements → design → tasks,
mechanically validated), and an embedded bi-temporal knowledge graph (pure-Go
SQLite, Graphiti-style) that doubles as the project's living documentation.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full design and decision log.

## Usage

```sh
go build -o rl .

# full pipeline from a challenge (csdd via npx if not on PATH)
rl run "fazer um jogo estilo wow" --csdd "npx -y @protonspy/csdd"

# see the end-to-end plan without spawning anything
rl run "<challenge>" --dry-run

# debug the inner loop over one approved csdd spec
rl run specs/<feat>

# inspect a spec's next actionable task
rl plan specs/<feat>

# render the living documentation graph (Mermaid or DOT)
rl graph export [--format dot]

# serve the knowledge graph to agents over MCP (stdio)
rl graph mcp
```

Requirements: `git`, `claude` (the brain) on PATH; csdd reachable (PATH or npx).

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

The full pipeline is covered by an offline integration test
(`internal/manager/pipeline_test.go`) that runs every phase against fake
`claude`/`csdd` binaries.
