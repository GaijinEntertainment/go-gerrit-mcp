# Tasks: go-gerrit-mcp — configurable, publishable Gerrit MCP server

- **Date:** 2026-07-10
- **Frame:** [01-go-gerrit-mcp.frame.md](./01-go-gerrit-mcp.frame.md)
- **Tracker:** GitHub Issues (this repository) — task numbers map 1:1 to issue numbers (#1–#23); phases are
  milestones; HITL tasks carry the `hitl` label; dependencies are native blocked-by links

## Phase 1: Tracer Bullet — `get_change` end-to-end

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| 1 | Scaffold repository | ~2.5h | — | green CI on empty module | AFK |
| 2 | Implement llmxml builder package | ~1.5h | #1 | package + passing tests | AFK |
| 3 | Implement configuration loading | ~2h | #1 | package + passing tests | AFK |
| 4 | Implement Gerrit client wrapper | ~2h | #1 | package + passing tests | AFK |
| 5 | Implement `get_change` tool, registry, stdio server | ~3h | #2 #3 #4 | callable tool over stdio | AFK |
| 6 | Verify tracer against live Gerrit | ~0.5h | #5 | confirmed gate, gap notes | HITL |

### Task 1: Scaffold repository

**Context:** everything else builds on the repo skeleton and CI; frame Phase 1.

**What to do:**

- Initialize the Go module `dev.gaijin.team/go/go-gerrit-mcp` with `cmd/go-gerrit-mcp/` and `internal/` layout
- Configure golangci-lint with the linter set the project will hold itself to
- Add GitHub Actions CI running lint, tests, and a build matrix across linux, darwin, and windows on every PR and
  push to main
- Add a repo CLAUDE.md that points at the glossary and states the build/test/lint commands
- Add `.gitignore`, LICENSE placeholder resolved with the repo owner if not already decided

**Acceptance criteria:**

- [ ] CI is green on an empty-but-building module across all three platforms
- [ ] Lint and test jobs fail the build when violations are introduced

**Artifact:** green CI run on the scaffolded repository.

**References:** [frame](./01-go-gerrit-mcp.frame.md), [alignment](./01-go-gerrit-mcp.alignment.md)

### Task 2: Implement llmxml builder package

**Context:** every tool renders output through llmxml; frame Phase 1, [ADR 1.3](./adr/1.3-llmxml-output.md).

**What to do:**

- Build an element builder producing line-structured XML-subset text: attributes with quoted scalar values,
  self-closing empty elements, inline text content, and wrapped (newline-delimited) child content
- No indentation, no escaping, no parsing, no `encoding/xml` — single-pass string construction
- Content is one-shot: setting content twice on an element is a programming error and must fail loudly

**Acceptance criteria:**

- [ ] Unit tests cover attribute quoting, inline vs wrapped content, empty elements, and content-reuse failure
- [ ] Package has no dependencies beyond the standard library

**Artifact:** llmxml package with passing tests.

**References:** [ADR 1.3](./adr/1.3-llmxml-output.md), glossary entry `llmxml`

### Task 3: Implement configuration loading

**Context:** the config surface is the product's safety contract; frame Phase 1,
[ADR 1.2](./adr/1.2-safe-by-default-posture.md).

**What to do:**

- Load connection identity from env only: `GERRIT_URL`, `GERRIT_USERNAME`, `GERRIT_TOKEN` — all required
- Implement the behavior-flag pattern: `--groups` with `GERRIT_MCP_GROUPS` mirror, flag winning over env; default
  `read`
- Validate eagerly and report all configuration errors at once; unknown group names fail startup
- Leave extension points for the remaining behavior flags (filters, projects, own-changes) without implementing them

**Acceptance criteria:**

- [ ] Precedence covered by tests: flag beats env, env beats default
- [ ] Missing secrets produce a single aggregated error naming every missing variable
- [ ] Zero-config run (secrets only) resolves to the `read` group

**Artifact:** config package with passing tests.

**References:** [alignment](./01-go-gerrit-mcp.alignment.md) (config surface), [ADR 1.2](./adr/1.2-safe-by-default-posture.md)

### Task 4: Implement Gerrit client wrapper

**Context:** all Gerrit traffic flows through one seam that later carries scoping and own-changes enforcement; frame
Phase 1.

**What to do:**

- Wrap the `andygrunwald/go-gerrit` client: HTTP Basic auth from config, startup credential validation via the
  self-account endpoint, sane HTTP timeout
- Recover Gerrit error response bodies on non-2xx results — the library returns only the status line; the wrapper
  must surface Gerrit's own message to callers
- Write the learning test first: confirm the library's response body is still readable after its error path, and
  design the recovery around the verified behavior

**Acceptance criteria:**

- [ ] Learning test documents the library's error-body behavior
- [ ] Wrapper tests (fake Gerrit via `httptest`, XSSI-prefixed fixtures) cover auth header, `/a/` path prefix, and
      error-body recovery for 404 and 409
- [ ] Startup fails cleanly with an actionable message on bad credentials

**Artifact:** Gerrit client wrapper package with passing tests.

**References:** [research](./01-go-gerrit-mcp.research.md) (go-gerrit library section), [frame](./01-go-gerrit-mcp.frame.md)

### Task 5: Implement `get_change` tool, registry, stdio server

**Context:** the tracer bullet's tip — first tool wired through every layer; frame Phase 1.

**What to do:**

- Implement a capability registry mapping groups to tool sets; only `read` → `get_change` exists at this point, but
  the group-resolution shape must anticipate filters and bundled subsets
- Implement `get_change`: change identifier input; output covering status, owner, labels with votes, reviewers,
  current revision, change messages, and submit requirements, rendered as llmxml
- Wire the official go-sdk stdio server: config → client wrapper → registry → registered tools; logs to stderr only

**Acceptance criteria:**

- [ ] Tool schema derives from a typed input struct
- [ ] Handler tests against fake Gerrit assert request options and llmxml output shape
- [ ] Server starts, lists exactly one tool, and shuts down cleanly on SIGINT/SIGTERM

**Artifact:** runnable server binary exposing `get_change` over stdio.

**References:** [frame](./01-go-gerrit-mcp.frame.md), [alignment](./01-go-gerrit-mcp.alignment.md) (tool inventory)

### Task 6: Verify tracer against live Gerrit

**Context:** frame Phase 1 verification gate — proof the full stack holds before depth is added.

**What to do:**

- Register the binary in a real MCP client against a live Gerrit instance; call `get_change` on a known change
- Exercise the failure paths: wrong token, nonexistent change
- Record any mismatch between expected and actual behavior as issues before Phase 2 starts

**Acceptance criteria:**

- [ ] Live `get_change` returns correct llmxml for a real change
- [ ] Startup with invalid credentials fails with an actionable error

**Artifact:** confirmation notes on the phase gate; follow-up issues for gaps, if any.

**References:** [frame](./01-go-gerrit-mcp.frame.md)

## Phase 2: Full `read` group + gating semantics

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| 7 | Implement `search_changes` | ~2h | #5 | tool + passing tests | AFK |
| 8 | Implement `list_change_files` and `get_file_diff` | ~2h | #5 | tools + passing tests | AFK |
| 9 | Implement `get_change_comments` | ~3h | #5 | tool + passing tests | AFK |
| 10 | Implement include/exclude tool filters | ~2h | #5 | filters + no-escalation tests | AFK |
| 11 | Implement project scoping enforcement | ~3h | #5 | wrapper enforcement + tests | AFK |
| 12 | Verify `read` group + scoping against live Gerrit | ~0.5h | #7 #8 #9 #10 #11 | confirmed gate | HITL |

### Task 7: Implement `search_changes`

**Context:** the query entry point of the `read` group; frame Phase 2.

**What to do:**

- Accept a Gerrit query string, an optional result limit, and a pagination offset; surface whether more results exist
  (Gerrit's more-changes marker) so the agent can continue
- Return per-change summaries (number, subject, project, branch, status, owner, updated) as llmxml
- Route the query through the client wrapper so project scoping (separate task) applies automatically

**Acceptance criteria:**

- [ ] Pagination covered by tests, including the more-results marker on the last item
- [ ] Tool description documents the essential Gerrit query operators the agent may use

**Artifact:** `search_changes` tool with passing tests.

**References:** [research](./01-go-gerrit-mcp.research.md) (query endpoint and operators)

### Task 8: Implement `list_change_files` and `get_file_diff`

**Context:** file-level review data for the `read` group; frame Phase 2.

**What to do:**

- `list_change_files`: files of a change revision with diffstat-level data (status, lines inserted/deleted); default
  to the current revision, accept an explicit one
- `get_file_diff`: unified diff content of one file in a revision, handling the magic commit-message path and binary
  files gracefully

**Acceptance criteria:**

- [ ] Both tools handle the current-revision default and an explicit revision
- [ ] Diff output renders as llmxml preserving hunk structure legibly

**Artifact:** both tools with passing tests.

**References:** [research](./01-go-gerrit-mcp.research.md) (revision/files/diff endpoints)

### Task 9: Implement `get_change_comments`

**Context:** the review-conversation reader; frame Phase 2.

**What to do:**

- Fetch published comments of a change, group by file, reconstruct threads from reply references, and compute each
  thread's resolved state from its chronologically last comment
- Support the status filter: all / resolved / unresolved
- Render thread nesting and per-comment metadata (author, patch set, line or range, updated) as llmxml

**Acceptance criteria:**

- [ ] Table tests cover nested replies, orphan comments, unresolved-state inheritance, and filter behavior
- [ ] Comment identifiers appear in output — the comment group's reply flow depends on them

**Artifact:** `get_change_comments` tool with passing tests.

**References:** [research](./01-go-gerrit-mcp.research.md) (comments endpoints, thread mechanics)

### Task 10: Implement include/exclude tool filters

**Context:** the per-tool narrowing layer over group resolution; frame Phase 2,
[ADR 1.1](./adr/1.1-independent-capability-groups.md).

**What to do:**

- Add `--include-tools` / `--exclude-tools` (comma lists, env mirrors) applied after group resolution
- Include keeps only listed tools from the group-resolved set; exclude removes listed tools; include never activates
  anything outside the enabled groups
- Unknown tool names in either list fail startup with the offending names spelled out

**Acceptance criteria:**

- [ ] No-escalation property proven by test: including a tool from a disabled group does not expose it
- [ ] Composition of both filters covered by tests

**Artifact:** filter implementation with passing tests.

**References:** [ADR 1.1](./adr/1.1-independent-capability-groups.md), glossary entry `Tool filters`

### Task 11: Implement project scoping enforcement

**Context:** the visibility restriction; frame Phase 2 verification gate anchors here.

**What to do:**

- Add `--projects` (comma list, env mirror); empty means unscoped
- Enforce inside the client wrapper, not in tool handlers: inject a project clause into every change query regardless
  of what the agent composed; refuse direct operations on changes whose project is outside the allowlist, with an
  error naming the restriction
- The refusal must occur before any mutating request leaves the process

**Acceptance criteria:**

- [ ] Query rewriting asserted at the HTTP boundary (outgoing request inspection), not at the tool layer
- [ ] Out-of-scope direct fetch refused; in-scope operations unaffected
- [ ] Agent-supplied project clauses cannot widen the allowlist

**Artifact:** scoping enforcement with passing tests.

**References:** [alignment](./01-go-gerrit-mcp.alignment.md) (desired end state), glossary entry `Project scoping`

### Task 12: Verify `read` group + scoping against live Gerrit

**Context:** frame Phase 2 verification gate.

**What to do:**

- Exercise all five read tools against a live instance through an MCP client
- With `--projects` set, attempt to escape the allowlist via crafted queries and direct fetches; confirm containment
- File issues for gaps before Phase 3 starts

**Acceptance criteria:**

- [ ] All five tools return correct data live
- [ ] Scoping escape attempts fail

**Artifact:** confirmation notes; follow-up issues if any.

**References:** [frame](./01-go-gerrit-mcp.frame.md)

## Phase 3: `comment` group + own-changes restriction

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| 13 | Implement bundled read subsets in registry | ~2h | #10 | registry mechanics + tests | AFK |
| 14 | Implement own-changes enforcement | ~2h | #11 | wrapper primitive + tests | AFK |
| 15 | Implement `post_comments` | ~3h | #13 #14 | tool + passing tests | AFK |
| 16 | Verify comment flow against live Gerrit | ~0.5h | #15 | confirmed gate | HITL |

### Task 13: Implement bundled read subsets in registry

**Context:** groups are self-sufficient — each write group carries the read tools it needs; frame Phase 3,
[ADR 1.1](./adr/1.1-independent-capability-groups.md).

**What to do:**

- Extend group resolution so a group contributes both its own tools and its bundled read subset; enabling `comment`
  alone exposes exactly `get_change`, `get_change_comments`, and `post_comments`
- Union across groups deduplicates; filters keep applying after the union

**Acceptance criteria:**

- [ ] `--groups comment` (alone) exposes exactly the specified three tools
- [ ] `--groups read,comment` produces no duplicates
- [ ] Filters compose with bundling without escalation

**Artifact:** registry bundling with passing tests.

**References:** [ADR 1.1](./adr/1.1-independent-capability-groups.md), [alignment](./01-go-gerrit-mcp.alignment.md) (tool inventory)

### Task 14: Implement own-changes enforcement

**Context:** the default-on safety restriction gating trail-leaving operations; frame Phase 3,
[ADR 1.2](./adr/1.2-safe-by-default-posture.md).

**What to do:**

- Add `--own-changes-only` (boolean, env mirror, default `true`)
- Enforce in the client wrapper: before any trail-leaving call, compare the target change's owner against the
  authenticated account; refuse mismatches with an error explaining the restriction and the disabling flag
- The refusal must occur before any mutating request leaves the process

**Acceptance criteria:**

- [ ] Foreign-owner change refused by default; own change passes; explicit `false` disables the check
- [ ] Refusal produces no outgoing mutating HTTP request (asserted at the HTTP boundary)

**Artifact:** enforcement primitive with passing tests.

**References:** [ADR 1.2](./adr/1.2-safe-by-default-posture.md), glossary entries `Own-changes restriction`, `Trail-leaving`

### Task 15: Implement `post_comments`

**Context:** the comment group's single tool — one review submission call; frame Phase 3.

**What to do:**

- Write the learning test first: the Gerrit review-input and comment-input structures marshal to the documented REST
  shapes for the reply reference, the unresolved tri-state, notify control, and the comments map
- Implement the tool: optional top-level message plus a list of comments (file, optional line or range, message,
  optional reply-to identifier, optional resolved/unresolved intent), notify level; one SetReview call
- New threads and replies share the same input shape; resolution toggling rides the unresolved field

**Acceptance criteria:**

- [ ] Payload assertions cover: new file/line/range comments, threaded reply, resolve and unresolve, notify levels
- [ ] Tool refuses cleanly when the target comment identifier for a reply does not exist on the change

**Artifact:** `post_comments` tool with passing tests, including the learning test.

**References:** [research](./01-go-gerrit-mcp.research.md) (SetReview mechanics), [alignment](./01-go-gerrit-mcp.alignment.md)

### Task 16: Verify comment flow against live Gerrit

**Context:** frame Phase 3 verification gate.

**What to do:**

- On a live change: post a new inline comment, reply into an existing thread, resolve and unresolve it; confirm
  threading and states in the Gerrit UI
- Attempt a comment on a foreign change with the restriction on; confirm local refusal

**Acceptance criteria:**

- [ ] Threaded reply lands threaded; resolution state matches intent
- [ ] Foreign-change attempt refused without leaving the process

**Artifact:** confirmation notes; follow-up issues if any.

**References:** [frame](./01-go-gerrit-mcp.frame.md)

## Phase 4: `transition` group

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| 17 | Implement `set_vote` | ~1.5h | #13 #14 | tool + passing tests | AFK |
| 18 | Implement `transition_change` | ~2.5h | #13 #14 | tool + passing tests | AFK |
| 19 | Verify transitions on a sandbox change | ~0.5h | #17 #18 | confirmed gate | HITL |

### Task 17: Implement `set_vote`

**Context:** voting is the transition group's label mechanism; frame Phase 4.

**What to do:**

- Accept label name, numeric value, and an optional accompanying message; submit as a review with labels
- Surface Gerrit's rejection (unknown label, value outside permitted range, vote on merged change) verbatim from the
  recovered error body

**Acceptance criteria:**

- [ ] Payload and endpoint asserted against fake Gerrit; error paths surface Gerrit's own message
- [ ] Own-changes and project scoping apply exactly as for comments

**Artifact:** `set_vote` tool with passing tests.

**References:** [research](./01-go-gerrit-mcp.research.md) (vote endpoints), [alignment](./01-go-gerrit-mcp.alignment.md)

### Task 18: Implement `transition_change`

**Context:** all change-state transitions behind one action enum; frame Phase 4.

**What to do:**

- Single tool with action enum `submit | abandon | restore | wip | ready` plus optional message, mapped to the five
  Gerrit endpoints; the tool description carries the state diagram (NEW⇄ABANDONED, WIP⇄ready, NEW→MERGED)
- Blocked submits (failing submit requirements) must report Gerrit's reason verbatim

**Acceptance criteria:**

- [ ] Each action asserted against its endpoint and payload on fake Gerrit
- [ ] 409-style conflicts (blocked submit, restore of a merged change) surface Gerrit's message
- [ ] Own-changes and project scoping apply

**Artifact:** `transition_change` tool with passing tests.

**References:** [research](./01-go-gerrit-mcp.research.md) (lifecycle endpoints), [alignment](./01-go-gerrit-mcp.alignment.md)

### Task 19: Verify transitions on a sandbox change

**Context:** frame Phase 4 verification gate.

**What to do:**

- On a sandbox change: set and delete a vote, toggle WIP/ready, abandon and restore; attempt a blocked submit and
  confirm the reported reason
- Confirm `--groups transition` alone is self-sufficient (bundled `get_change` present)

**Acceptance criteria:**

- [ ] All five actions verified live; blocked submit reports Gerrit's reason
- [ ] Standalone `transition` group works without `read`

**Artifact:** confirmation notes; follow-up issues if any.

**References:** [frame](./01-go-gerrit-mcp.frame.md)

## Phase 5: Publishing

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| 20 | Write README | ~2h | #19 | README.md | AFK |
| 21 | Add release pipeline | ~2.5h | #1 | dry-run release + image smoke test | AFK |
| 22 | Publish MCP registry entry | ~1h | #21 | live registry entry | HITL |
| 23 | Cold-start validation | ~1h | #20 #21 | validated setup path | HITL |

### Task 20: Write README

**Context:** the public face; frame Phase 5.

**What to do:**

- Cover: what the server is, install paths (binary release, Docker, `go install`), MCP client configuration examples,
  the full flag/env reference, capability groups with the safety posture (read-only default, own-changes default) and
  how to widen it deliberately
- Document the target platform (Gerrit 3.13+, auth tokens) and the llmxml output contract at reader level

**Acceptance criteria:**

- [ ] Every flag and env var documented with default and mirror precedence
- [ ] Config examples are copy-pasteable into a clean MCP client and work

**Artifact:** README.md.

**References:** [alignment](./01-go-gerrit-mcp.alignment.md), ADRs 1.1–1.3

### Task 21: Add release pipeline

**Context:** binary distribution is the differentiator; frame Phase 5.

**What to do:**

- goreleaser configuration: binaries for linux/darwin/windows on tag push, plus a Docker image published to ghcr.io
- The image must start, print an actionable error, and exit non-zero when required env is missing

**Acceptance criteria:**

- [ ] Snapshot (dry-run) release produces all platform binaries
- [ ] Image smoke test passes in CI

**Artifact:** release workflow + goreleaser config, validated by dry run.

**References:** [frame](./01-go-gerrit-mcp.frame.md), [research](./01-go-gerrit-mcp.research.md) (distribution conventions)

### Task 22: Publish MCP registry entry

**Context:** discoverability through the official MCP registry; frame Phase 5.

**What to do:**

- Author `server.json` (name `team.gaijin/go-gerrit-mcp`, package entries for the binary/OCI distributions, env
  variable declarations) and publish via the registry's publisher tooling
- Complete the one-time domain-namespace verification for `gaijin.team` (DNS TXT or HTTP challenge) before publishing

**Acceptance criteria:**

- [ ] Registry entry resolves and lists the current release
- [ ] Declared env vars match the README

**Artifact:** live registry entry.

**References:** [research](./01-go-gerrit-mcp.research.md) (registry requirements)

### Task 23: Cold-start validation

**Context:** frame Phase 5 verification gate — the five-minute test.

**What to do:**

- On a machine without the repo: follow the README only, from install to a working `read`-group server in an MCP
  client against a real Gerrit
- Time it; note every point of friction; file issues for anything that breaks the five-minute budget

**Acceptance criteria:**

- [ ] Working server from README alone in under five minutes
- [ ] Friction points filed as issues

**Artifact:** validation notes + filed issues.

**References:** [frame](./01-go-gerrit-mcp.frame.md)

## Dependency Graph

```
Phase 1: [#1 scaffold]
             ├─→ [#2 llmxml] ──┐
             ├─→ [#3 config] ──┼─→ [#5 get_change+server] ─→ [#6 verify HITL]
             ├─→ [#4 wrapper] ─┘         │
             └─→ [#21 release pipeline]  │        (can start any time after #1)
                                         │
Phase 2:     ┌─→ [#7 search_changes] ────┤
             ├─→ [#8 files+diff] ────────┤
   #5 ───────┼─→ [#9 comments] ──────────┼─→ [#12 verify HITL]
             ├─→ [#10 filters] ──────────┤
             └─→ [#11 project scoping] ──┘
                       │
Phase 3:   [#10]─→ [#13 bundling] ─┬─→ [#15 post_comments] ─→ [#16 verify HITL]
           [#11]─→ [#14 own-only] ─┘
                       │
Phase 4:   [#13,#14] ─┬─→ [#17 set_vote] ────┬─→ [#19 verify HITL]
                      └─→ [#18 transition] ──┘
                       │
Phase 5:   [#19] ─→ [#20 README] ─┬─→ [#23 cold-start HITL]
           [#21 release] ─────────┤
           [#21] ─→ [#22 registry HITL]
```

Within-phase parallelism: #2/#3/#4; #7–#11; #13/#14; #17/#18; #20/#22. Task #21 is the only cross-phase early-start
(scaffolding-dependent only). HITL gates (#6, #12, #16, #19, #22, #23) punctuate each phase.
