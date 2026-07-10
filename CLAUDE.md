# go-gerrit-mcp

MCP server exposing Gerrit code review operations as capability-gated tools (`read` / `comment` / `transition`). Go,
stdio transport, llmxml output. Work is driven by `design-docs/` (brief, research, alignment, frame, tasks; ADRs in
`design-docs/adr/`); GitHub issues #1-#23 map 1:1 to the task breakdown.

## Vocabulary

`docs/glossary.md` defines canonical terms and forbidden aliases (capability groups, trail-leaving, llmxml, scoping).
Use it before naming anything user-facing.

## Commands

- Build: `go build ./...`
- Test: `go test -race ./...`
- Lint: `golangci-lint run`
- Format: `golangci-lint fmt`

## Constraints

- Tool output is llmxml (ADR 1.3) — never `encoding/xml`, never JSON output.
- Safety defaults are contractual (ADR 1.2): zero config = `read` group only; `--own-changes-only` defaults to `true`.
- Dependencies: add only when needed, always at the latest published version — never versions from memory.
- `design-docs/` and `docs/` ship publicly — no internal hostnames, local paths, or company-internal references.
