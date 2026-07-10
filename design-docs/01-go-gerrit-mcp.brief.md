# Brief: go-gerrit-mcp — configurable, publishable Gerrit MCP server

- **Date:** 2026-07-10
- **Stage:** Discovery (DRAFT pipeline)

## Motivation

No viable Gerrit MCP exists publicly, while agent-driven review workflows need one. Build — ground-up — a
configurable, safe-by-default, open-source Gerrit MCP server for anyone running a Gerrit instance.

## Goals

- Open-source publication for a general audience
- Pure on-demand MCP tools centered on Gerrit **changes** — no background behavior
- Three independent, self-sufficient capability groups: `read`, `comment`, `transition`; each write group bundles the
  minimal change-read subset it needs to function; groups union; all groups = full capability set
- Tool include/exclude filters applied after group resolution; include never escalates beyond groups
- Project allowlist scoping everything, including forced injection of project clauses into search queries
- Own-changes restriction gating trail-leaving ops (comment, transition) — **on by default**, disabled by flag
- Zero config → `read` group only
- Config split: flags = behavior (groups, filters, scoping), env vars = secrets; no config files

## Non-goals (v1)

- Polling / notification channels / review-waiting — dedicated future question, redesigned when revisited
- Thread-author reply filter — deferred until proven worthwhile
- Accounts, groups, repo-content browsing read APIs
- Multi-host support (creds differ per host; one process per host)
- Gerrit query-syntax teaching skill — separate side quest, not this repo

## Constraints

- Go, single Gerrit host per process
- Safest option wins wherever a default is ambiguous

## Key risks

- Agent leaves a trail on colleagues' changes → own-changes default-on mitigates
- Agent bypasses project scoping via crafted search queries → server-side query injection mitigates
- Gerrit query syntax is unintuitive for agents → acknowledged, mitigation out of scope

## Flagged term ambiguities

- `read` / `comment` / `transition` — new canonical group vocabulary for the glossary
- "trail-leaving" — the criterion separating gated from ungated ops; worth a glossary entry
- Rebase exposure within `transition` — explicitly uncertain

## Questions for research

- Gerrit REST API inventory per group, incl. the minimal read subset each write group needs
- Existing Gerrit MCP landscape — verify the "no viable option" assumption
- Config conventions in published MCP servers (flags/env split, naming)
- Gerrit auth methods and version-compatibility considerations
- MCP transport for publishing: stdio vs streamable HTTP

## Not yet specified

- Review-waiting/notification mechanism (post-v1; only an idea exists)
- Env-mirrors-all-flags with override precedence — soft idea, alignment decides
- Exact per-group tool inventory
