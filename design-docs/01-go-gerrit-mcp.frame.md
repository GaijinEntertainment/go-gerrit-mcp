# Frame: go-gerrit-mcp — configurable, publishable Gerrit MCP server

- **Date:** 2026-07-10
- **Alignment:** [01-go-gerrit-mcp.alignment.md](./01-go-gerrit-mcp.alignment.md)

Layer stack every slice crosses: config (flags/env) → capability registry → Gerrit client wrapper → tool handler →
llmxml rendering → MCP stdio surface.

## Phase 1: Tracer Bullet — `get_change` end-to-end

**Components:**

- Repo scaffolding — `dev.gaijin.team/go/go-gerrit-mcp` go.mod, `cmd/go-gerrit-mcp/`, `internal/`, golangci-lint
  config, test harness, GitHub Actions CI (lint + test + build matrix: linux/darwin/windows), repo CLAUDE.md (points
  at `docs/glossary.md`)
- `internal/config` — env secrets (`GERRIT_URL`/`GERRIT_USERNAME`/`GERRIT_TOKEN`, required), `--groups` flag with
  `GERRIT_MCP_GROUPS` mirror (flag wins), default `read`; fail-fast validation, all errors at once
- `internal/gerritclient` — wraps go-gerrit: basic auth, startup self-account check, error-body recovery on non-2xx
  (the library discards it); the future seam for scoping enforcement
- `internal/llmxml` — element builder: line-structured, no indentation, no escaping, one-shot content setters
- `internal/registry` — group → tool-set resolution; only `read` group with only `get_change` for now
- `internal/tools` — `get_change` handler: change-id input → ChangeInfo (detailed labels/accounts, current revision,
  messages, submit requirements) → llmxml
- `cmd/go-gerrit-mcp` — wire config → client → registry → official go-sdk stdio server; logs to stderr

**Testing strategy:**

- llmxml: unit tests (attr quoting, inline vs wrapped content, panic on content reuse)
- config: unit tests (precedence flag>env, defaults, missing-secret errors)
- gerritclient + handler: `httptest` fake Gerrit serving XSSI-prefixed (`)]}'`) fixtures; assert auth header, `/a/`
  prefix, error-body recovery on 404/409
- Learning test: go-gerrit's `*Response` body is still readable after `CheckResponse` returns an error

**Verification gate:**

- Built binary registered in an MCP client (Claude Code) lists exactly one tool and `get_change` returns llmxml for a
  live change; startup fails cleanly with wrong credentials

**Acceptance criteria:**

- [ ] `go build ./...`, `go test ./...`, `golangci-lint run` pass locally and in CI across the platform matrix
- [ ] `get_change` works end-to-end over stdio against a real Gerrit
- [ ] Zero config beyond secrets yields the `read` group only

## Phase 2: Full `read` group + gating semantics

**Components:**

- `internal/tools` — `search_changes` (query, limit, pagination via `_more_changes`), `list_change_files`,
  `get_file_diff`, `get_change_comments` (thread structure from `in_reply_to` chains, resolved state from
  chronologically-last comment, all/resolved/unresolved filter)
- `internal/config` + `internal/registry` — `--include-tools`/`--exclude-tools` (+ env mirrors): applied after group
  resolution, narrowing only; unknown tool names in filters fail startup
- `internal/gerritclient` — project scoping: `--projects` (+ mirror); project clause injected into every change query
  regardless of agent input; direct operations on out-of-scope changes refused with an explanatory error

**Testing strategy:**

- Fake-Gerrit fixtures per endpoint; table tests for thread resolution (nested replies, orphan comments, unresolved
  inheritance) and pagination
- Scoping tests: query rewriting asserted on the outgoing request; out-of-scope `get_change` refused
- Filter tests: include/exclude composition, no-escalation property (include of a non-`read` tool ≠ activation)

**Verification gate:**

- With `--projects` set, `search_changes` provably cannot escape the allowlist (asserted at the HTTP boundary, not the
  tool layer)

**Acceptance criteria:**

- [ ] All five `read` tools work against a real Gerrit
- [ ] Filters narrow, never grant; startup rejects unknown tool names
- [ ] Project scoping enforced on queries and direct fetches

## Phase 3: `comment` group + own-changes restriction

**Components:**

- `internal/registry` — bundled read subsets: `comment` alone exposes `get_change` + `get_change_comments` +
  `post_comments`; union with other groups deduplicates
- `internal/gerritclient` — own-changes enforcement primitive: change owner vs authenticated account, checked before
  every trail-leaving call; `--own-changes-only` (+ mirror), default `true`
- `internal/tools` — `post_comments`: one SetReview call — optional top-level message, inline/file/range comments,
  replies (`in_reply_to`), resolve/unresolve (`unresolved`), notify control

**Testing strategy:**

- Fake-Gerrit assertions on the SetReview payload (comments map shape, `in_reply_to`, `unresolved` tri-state, notify)
- Own-changes tests: foreign-owner change → refusal with explanatory error; own change → passes; flag off → passes
- Registry tests: `--groups comment` tool list exactly as specified; `read,comment` union has no duplicates
- Learning test: go-gerrit `ReviewInput`/`CommentInput` marshal to the documented REST shapes

**Verification gate:**

- Against a real Gerrit: reply to an existing thread lands threaded and resolves it; attempt on a foreign change is
  refused locally (no HTTP call leaves the process)

**Acceptance criteria:**

- [ ] `post_comments` covers new threads, replies, resolution toggling in one call
- [ ] Own-changes restriction on by default, disabled only by explicit flag
- [ ] Bundled read subset works without the `read` group enabled

## Phase 4: `transition` group

**Components:**

- `internal/tools` — `set_vote` (label, value, optional message via SetReview `labels`), `transition_change` (action
  enum `submit|abandon|restore|wip|ready` + optional message, mapped to the five endpoints; description carries the
  state diagram)
- `internal/registry` — `transition` group with bundled `get_change`

**Testing strategy:**

- Fake-Gerrit per action: endpoint + payload asserted; 409 paths (submit blocked by label, vote on merged change)
  surface Gerrit's error body to the agent
- Own-changes tests reused across both tools

**Verification gate:**

- Against a real Gerrit sandbox change: vote set and removed, WIP toggled, abandon/restore round-trip; submit refusal
  (blocked) reports Gerrit's reason verbatim

**Acceptance criteria:**

- [ ] Both tools functional; all five transition actions covered
- [ ] `--groups transition` alone is self-sufficient (bundled `get_change`)
- [ ] Trail-leaving gating identical to Phase 3 behavior

## Phase 5: Publishing

**Components:**

- README — install (binary, Docker, `go install`), MCP client config examples, full flag/env reference, capability
  group and safety-posture documentation
- Release workflow — goreleaser binaries (darwin/linux/windows), Docker image on ghcr.io; builds on the CI
  established in Phase 1
- MCP registry — `server.json` under `team.gaijin/go-gerrit-mcp` (domain-verified namespace), published via
  `mcp-publisher`
- LICENSE, versioning start (v0.x until API settles)

**Testing strategy:**

- Release dry-run (goreleaser snapshot); Docker image smoke test (starts, fails cleanly without env)
- README config examples validated by copy-pasting into a clean MCP client

**Verification gate:**

- A colleague-grade cold start: clean machine, README only, working `read`-group server in under five minutes

**Acceptance criteria:**

- [ ] Tagged release publishes binaries + image automatically
- [ ] MCP registry entry live
- [ ] README covers all flags, env vars, groups, and safety defaults

## Learning Tests

- go-gerrit error handling — `*Response` body readable after `CheckResponse` error (Phase 1; wrapper design rests on it)
- go-gerrit `ReviewInput`/`CommentInput` — marshaled JSON matches documented REST shapes for `in_reply_to`,
  `unresolved`, `notify`, `labels` (Phase 3; comment/vote correctness rests on it)

## Phase Sequence

Phase 1 (tracer bullet, no deps)
    ↓
Phase 2 (depends on Phase 1)
    ↓
Phase 3 (depends on Phase 2: filters/scoping infra)
    ↓
Phase 4 (depends on Phase 3: own-changes primitive, bundling mechanics)
    ↓
Phase 5 (depends on all)

Strictly linear: each phase consumes infrastructure the previous one introduces (registry → scoping/filters →
own-changes/bundling). Parallelism inside phases (per-tool) is possible; across phases it is not worth the seams.

## Scope Boundaries

**In scope:** everything in the alignment end state — 8 tools across 3 groups, filters, project scoping, own-changes
restriction, stdio transport, llmxml output, binary/Docker/registry distribution.

**Out of scope (post-v1):** review-waiting/notification mechanism, thread-author reply filter, Streamable HTTP /
remote mode, draft-comment CRUD, reviewer management, topic/hashtag/attention-set, rebase/cherry-pick/move/revert,
delete operations, whole-change patch, multi-host.
