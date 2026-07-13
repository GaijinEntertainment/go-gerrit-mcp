# Frame: Review notifications

- **Date:** 2026-07-13
- **Alignment:** design-docs/02-review-notifications.alignment.md

Naming fixed by this frame (per glossary): tools `subscribe_change` / `unsubscribe_change`; flag family
`--review-notifications` (enable), `--review-notifications-poll-interval`, `--review-notifications-include-own`,
`--review-notifications-exclude-accounts`, `--review-notifications-exclude-patterns`, each with the matching
`GERRIT_MCP_REVIEW_NOTIFICATIONS*` mirror. The `--notify-*` prefix is deliberately avoided — `notify` already means
email-notify levels on review posting. New package: `internal/notifications` (subscription store, poller, filters,
payload rendering); transport wrapper lives beside the server assembly.

## Phase 1: Tracer Bullet — subscribe, poll, push

The thinnest wire through every layer: one flag, one tool, a poller that detects only `updated` movement, a minimal
payload, and a channel notification that reaches a real client.

**Components:**

- `internal/config` — `--review-notifications` behaviorFlag (+ mirror); interval flag with 60s fallback.
- `internal/notifications` — subscription store (mutex-guarded map keyed by change number, per-change cursor =
  last-seen `updated`); poller goroutine: batched `change:A OR change:B` query per tick, emits a bare event when
  `updated` moved.
- `internal/tools` — `subscribe_change` tool (validates the change exists and is in project scope via
  `GetChange`, stores it, returns llmxml ack naming current status and patch set).
- `cmd/go-gerrit-mcp` — capability declaration (`Experimental["claude/channel"]`) and conditional instructions
  sentence when enabled; wrapping transport capturing the `mcp.Connection`; poller lifecycle bound to the signal
  context.
- Emission — ID-less `jsonrpc.Request`, method `notifications/claude/channel`, params `{content, meta}`; content is
  a minimal `<review_activity change="..." status="...">` element for now.

**Testing strategy:**

- Learning tests first (see Learning Tests) — SDK capability override and raw-write behavior are assumptions until
  executed.
- Unit: store add/duplicate/remove; poller tick against `httptest` Gerrit stub (updated moved / not moved / query
  error → logged and retried).
- Manual probe: JSON-RPC pipe with a subscription, mutate the change live, observe the notification line on stdout.

**Verification gate:**

- Live end-to-end against Claude Code: `claude --dangerously-load-development-channels server:gerrit`, subscribe to
  a sandbox change, post a comment on it from the UI, the `<channel source="gerrit">` block appears in the session.
  This gate validates the entire contract stack before any depth is built.

**Acceptance criteria:**

- [ ] Zero-config server is byte-identical to today: no tool, no capability, no instructions change, no goroutine.
- [ ] With the flag on, subscribing then mutating the change produces exactly one notification per poll tick with
      movement; quiet ticks produce nothing; empty subscription set skips the query entirely.

## Phase 2: Real activity deltas and terminal states

**Components:**

- `internal/notifications` — cursor grows per-kind high-water marks; event extraction from `GetChange` detail +
  `ListChangeComments`: new change messages (author, date, tag, revision), new votes (`ApprovalInfo.Date` newer
  than cursor, value, label), new/updated comment threads (reuse the thread pipeline), status transitions.
- `internal/tools` — `unsubscribe_change` tool; payload rendering: `<review_activity>` composing existing
  vocabulary — `<message>`, `<vote>`, thread/`<comment>` elements exactly as `get_change_comments` renders them;
  `meta` carries `change`, `kind`, `project`.
- Terminal handling — merged/abandoned detected from status: final notification carries the transition plus
  `subscription="ended"` semantics in prose, change leaves the store.

**Testing strategy:**

- Golden tests for payload rendering (existing golden harness in `internal/tools`).
- Unit: cursor semantics — replay the same poll twice, second emits nothing; vote-only update emits a vote event;
  comment burst groups into one payload.
- Live probe against the production incident change for thread-render parity.

**Verification gate:**

- A subscribed change taken through comment, reply, vote, and submit on a sandbox produces the expected
  notification sequence ending in the auto-unsubscribe notice, and the store is empty afterwards.

**Acceptance criteria:**

- [ ] Every activity kind (message, vote, thread, transition) renders and pushes; nothing repeats across ticks.
- [ ] Terminal states always end the subscription with the announcing notification, including when the terminal
      transition and other activity arrive in the same tick.

## Phase 3: Filters and model-facing prompts

**Components:**

- `internal/config` — `include-own`, `exclude-accounts`, `exclude-patterns` flags; regex compilation at load with
  aggregated fail-loud errors.
- `internal/notifications` — filter chain applied per extracted event: self-authorship (default drop, flag keeps),
  excluded accounts (username or numeric ID), content regexes against message/comment text. Filtered-to-empty
  ticks emit nothing.
- `internal/tools` / `cmd` — full instructions section: when to subscribe, what arrives, auto-unsubscribe meaning,
  re-subscribe-after-restart guidance; tool descriptions final.

**Testing strategy:**

- Unit: table tests per filter and their composition; config tests for invalid regex (startup fails naming the
  pattern) and mirror resolution.
- Golden: instructions output with the feature on/off.

**Verification gate:**

- Live: a bot-authored comment matching an exclusion pattern produces no notification while a human reply in the
  same tick does.

**Acceptance criteria:**

- [ ] Own activity silent by default, delivered with the include flag.
- [ ] Account and pattern exclusions drop matching events only; invalid regex aborts startup with an aggregated
      error naming the pattern.

## Phase 4: Hardening and operator docs

**Components:**

- `internal/notifications` — poll failure policy (log, keep subscription, retry next tick; repeated failures do not
  kill the poller), change-became-inaccessible handling (scope loss / deletion → end subscription with notice),
  clean shutdown ordering on context cancellation.
- `README.md` — feature section: flags table rows, channel enablement (`--channels` / research-preview
  `--dangerously-load-development-channels server:<name>`), org-policy caveat, per-project configuration synergy.
- `docs/` — glossary already updated; verify vocabulary consistency across tool descriptions and README.

**Testing strategy:**

- Unit: error-path tests (Gerrit 5xx, 404 on subscribed change, ctx cancellation mid-tick).
- `go test -race` across the package (first concurrent component in the codebase).

**Verification gate:**

- Kill/restart and failure-injection probes leave no goroutine leaks (`-race` clean, poller exits with the session).

**Acceptance criteria:**

- [ ] Poller survives transient Gerrit failures and shuts down cleanly with the session.
- [ ] README documents the feature end-to-end including research-preview caveats.

## Learning Tests

- **SDK capabilities override** — setting `ServerOptions.Capabilities` with `Experimental["claude/channel"]`
  preserves the inferred tools capability (research flags the interaction; verify before Phase 1 builds on it).
- **Raw notification write** — an ID-less `jsonrpc.Request` written via the wrapped `mcp.Connection` concurrently
  with SDK traffic is framed correctly and received by a client (probe with a stub client before trusting it).
- **Channel injection** — Claude Code with the development flag renders our notification as a `<channel>` block
  (Phase 1 gate doubles as this test).
- **Gerrit vote messages** — a bare vote on the production instance produces a `ChangeMessageInfo` and a dated
  `ApprovalInfo`; default query results include `updated` without `o=` options.

## Phase Sequence

Phase 1 (tracer bullet, no deps)
    ↓
Phase 2 (depends on Phase 1)
Phase 3 (config/filter work can start against Phase 1; filter-chain integration depends on Phase 2's event model)
    ↓
Phase 4 (depends on Phases 2–3)

## Scope Boundaries

**In scope:** subscription tools, poller, filters, payload rendering, channel emission, operator docs.
**Out of scope:** multiple notification channels and per-channel configuration; subscription persistence;
attention-set/stream-events/webhook sources; permission relay; official channel-plugin packaging.
