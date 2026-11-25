# go-gerrit-mcp

MCP server exposing Gerrit code review operations as capability-gated tools (`read` / `comment` / `transition`). Go,
stdio transport, llmxml output. `plugins/gerrit-review-notifications/` is the Claude Code mod that delivers review
notifications; it has its own `CLAUDE.md`. Work is driven by `design-docs/` (brief, research, alignment, frame, tasks;
ADRs in `design-docs/adr/`); GitHub issues #1-#23 map 1:1 to the task breakdown.

## Vocabulary

`docs/glossary.md` defines canonical terms and forbidden aliases (capability groups, trail-leaving, llmxml, scoping).
Use it before naming anything user-facing.

## Commands

- Build: `go build ./...`
- Test: `go test -race ./...`
- Lint: `golangci-lint run`
- Format: `golangci-lint fmt`
- Plugins, from `plugins/`: `vp check` (format of the code; lint and types of the files in `plugins/` itself),
  `vp exec rumdl fmt .` (markdown), `vp run plugins` (validates the marketplace and every plugin, runs every mod's
  tests). No CI step lints or type-checks a mod's own sources: their types exist only where Claude Code loaded the mod

## Constraints

- Tool output is llmxml (ADR 1.3) — never `encoding/xml`. JSON output only from `get_review_activity`, which a program
  reads.
- Safety defaults are contractual (ADR 1.2): zero config = `read` group only; `--own-changes-only` defaults to `true`.
- Dependencies: add only when needed, always at the latest published version — never versions from memory.
- `design-docs/` and `docs/` ship publicly — no internal hostnames, local paths, or company-internal references.
