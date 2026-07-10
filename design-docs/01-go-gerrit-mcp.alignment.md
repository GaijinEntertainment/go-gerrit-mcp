# Alignment: go-gerrit-mcp — configurable, publishable Gerrit MCP server

- **Date:** 2026-07-10
- **Brief:** [01-go-gerrit-mcp.brief.md](./01-go-gerrit-mcp.brief.md)
- **Research:** [01-go-gerrit-mcp.research.md](./01-go-gerrit-mcp.research.md)
- **Glossary:** [docs/glossary.md](../docs/glossary.md) (created during this alignment)

Versions named in research are snapshots; no dependency version is pinned by design — everything resolves to latest
at the moment of application.

## Patterns Adopted

- **Official `modelcontextprotocol/go-sdk`** — MCP protocol layer. Stable v1 line with a no-breaking-changes
  guarantee; struct-tag schema derivation; stdio + Streamable HTTP, so a later remote mode is additive.
  mark3labs/mcp-go rejected: pre-v1, nothing unique needed from it.
- **`andygrunwald/go-gerrit` behind a thin internal wrapper** — Gerrit REST client. Covers every endpoint all three
  groups need. The wrapper is load-bearing: recovers Gerrit error bodies the library discards (its issue #64), and is
  the single seam for project-scoping query injection and own-changes checks. LUCI client rejected (proto-coupled,
  heavyweight).
- **Config: flags > env; env-only for secrets** — behavior lives in flags with `GERRIT_MCP_*` env mirrors (flag wins,
  per the universal precedence hierarchy); connection identity/secrets live in env only: `GERRIT_URL`,
  `GERRIT_USERNAME`, `GERRIT_TOKEN` (vendor-prefix ecosystem shape). No config files. *Declared deviation:* strict CLI doctrine
  forbids secrets in env; MCP clients can only pass `args`+`env` and stdin is the JSON-RPC channel — the entire
  ecosystem does env credentials, and we follow it knowingly.
- **Capability gating: `--groups read,comment,transition` + `--include-tools`/`--exclude-tools`** — the ecosystem's
  named-toolset-group pattern with our vocabulary and semantics (filters never escalate; default `read`).
  See [ADR 1.1](./adr/1.1-independent-capability-groups.md).
- **snake_case verb_noun tools, deliberately small inventory** — ecosystem-dominant naming; Anthropic guidance on
  tool count. v1 ships 8 tools.
- **stdio transport only in v1** — publishing target is local spawn by MCP clients; Streamable HTTP additive later.
- **llmxml output** — LLM-digestible XML subset, purpose-built renderer, not `encoding/xml`.
  See [ADR 1.3](./adr/1.3-llmxml-output.md).
- **Standalone module `dev.gaijin.team/go/go-gerrit-mcp`** (vanity import; repo GaijinEntertainment/go-gerrit-mcp) —
  `cmd/go-gerrit-mcp/` + `internal/`, standard Go layout.
- **Distribution: binary releases + Docker (ghcr.io) + MCP registry `server.json`** — goreleaser; registry name
  `team.gaijin/go-gerrit-mcp` (domain-verified namespace). No existing Gerrit MCP ships binaries — cheapest real
  differentiator.

## Current State

An empty repository — a ground-up build. In the public landscape, no existing Gerrit MCP offers capability gating
or binary distribution, the single actively-maintained option is Python- and curl-based, and the only Go
implementation is an abandoned read-only server (research, landscape section). Every artifact starts here.

## Desired End State

Standalone stdio MCP server, one Gerrit host per process. At startup: resolve groups → build tool registry → apply
include/exclude filters (narrowing only) → register with the SDK. All Gerrit traffic passes the internal client
wrapper, which enforces project scoping (project clause injected into every change query regardless of agent input;
direct operations on out-of-scope changes refused) and the own-changes restriction (trail-leaving calls refused on
changes not owned by the authenticated account; `--own-changes-only` default `true`).

### Config surface

| Concern | Flag | Env mirror | Default |
|---|---|---|---|
| Capability groups | `--groups` | `GERRIT_MCP_GROUPS` | `read` |
| Tool include filter | `--include-tools` | `GERRIT_MCP_INCLUDE_TOOLS` | empty (no narrowing) |
| Tool exclude filter | `--exclude-tools` | `GERRIT_MCP_EXCLUDE_TOOLS` | empty |
| Project scoping | `--projects` | `GERRIT_MCP_PROJECTS` | empty (unscoped) |
| Own-changes restriction | `--own-changes-only` | `GERRIT_MCP_OWN_CHANGES_ONLY` | `true` |
| Connection + credentials | — | `GERRIT_URL`, `GERRIT_USERNAME`, `GERRIT_TOKEN` | required |

### Tool inventory (8)

| Group | Tool | Purpose |
|---|---|---|
| read | `search_changes` | Change query with limit/pagination; project clause forced under scoping |
| read | `get_change` | Detail: status, owner, labels+votes, reviewers, revisions, submit requirements, change messages (top-level review messages live here, not in comments) |
| read | `list_change_files` | Files of a revision with diffstat |
| read | `get_file_diff` | Per-file diff of a revision |
| read | `get_change_comments` | Published inline/file comments, thread structure, resolved state, status filter |
| comment | `post_comments` | One SetReview call: optional top-level message + inline/file/range comments; replies via `in_reply_to`; resolve/unresolve; notify control |
| transition | `set_vote` | Label + value + optional message |
| transition | `transition_change` | Action enum `submit \| abandon \| restore \| wip \| ready` + optional message — all change-state transitions in one tool |

Bundled read subsets: `comment` → `get_change`, `get_change_comments`; `transition` → `get_change`.

Excluded from v1 (additive later if warranted): draft-comment CRUD, reviewer management, topic/hashtag/attention-set,
rebase, cherry-pick/move/revert, delete operations, whole-change patch.

### Target platform

Gerrit 3.13+ (the target instance runs it): docs speak auth tokens, not HTTP passwords; modern read options
(submit requirements) assumed present. REST mechanics are version-stable (`/a/` + Basic, additive API policy).

## Resolved Questions

- Third group name → `transition` (verb-parallel with `read`/`comment`; the members transition the change)
- Groups model → independent + self-sufficient, union, filters never escalate ([ADR 1.1](./adr/1.1-independent-capability-groups.md))
- Default posture → `read` only; own-changes on by default ([ADR 1.2](./adr/1.2-safe-by-default-posture.md))
- Flag/env precedence → flag wins; behavior mirrored in `GERRIT_MCP_*`, secrets env-only
- Rebase → out of v1 entirely
- WIP/ready → in, as `transition_change` actions
- State transitions → single `transition_change` tool with action enum; `set_vote` separate (votes are not state
  transitions, and merging them tangles two permission concerns)
- Whole-change patch tool → not needed; `list_change_files` + `get_file_diff` suffice
- Own-changes flag shape → boolean `--own-changes-only`, default `true`
- Output format → llmxml ([ADR 1.3](./adr/1.3-llmxml-output.md))
- Module path → `dev.gaijin.team/go/go-gerrit-mcp` vanity import
- "No viable Gerrit MCP" assumption → qualified: an official Python server exists (curl-based, config-file-driven);
  no Go implementation with traction, none capability-gated, none binary-distributed

## Not Yet Specified

- Review-waiting/notification mechanism — post-v1, will be redesigned from scratch; only an idea exists
- Thread-author reply filter — post-v1, deferred until proven worthwhile
- Gerrit query-syntax agent skill — separate side quest, outside this repo
- Streamable HTTP / remote mode — additive post-v1 option, not designed yet

## Recorded ADRs

- [ADR 1.1: Independent, self-sufficient capability groups](./adr/1.1-independent-capability-groups.md)
- [ADR 1.2: Safe-by-default posture](./adr/1.2-safe-by-default-posture.md)
- [ADR 1.3: llmxml tool output](./adr/1.3-llmxml-output.md)
