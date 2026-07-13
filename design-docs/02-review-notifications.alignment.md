# Alignment: Review notifications

- **Date:** 2026-07-13
- **Brief:** design-docs/02-review-notifications.brief.md
- **Research:** design-docs/02-review-notifications.research.md
- **Glossary:** docs/glossary.md (entries added: Review notifications, Subscription, Channel)

## Patterns Adopted

- **behaviorFlag flag/mirror resolution** — every new option (enable, account exclusions, content-pattern
  exclusions, include-own, poll interval) joins `config.Load` as a `behaviorFlag` with a `GERRIT_MCP_*` mirror.
  Found in internal/config/config.go:88-117, universal (5/5 existing flags). Errors aggregate via `errors.Join`;
  invalid exclusion regexes fail startup loudly through the same path.
- **Disabled = never constructed** — subscribe/unsubscribe tools are registered only when the enable flag is on,
  mirroring how group-gated tools are simply never registered (cmd/go-gerrit-mcp/main.go:77-81). Gating is by the
  dedicated flag, not by `registry.Resolve`: this is not a capability group.
- **llmxml rendering reuse** — notification payloads reuse the comment-thread pipeline and shared helpers
  (`accountLabel`, `timestamp`; internal/tools/get-change-comments.go, get-change.go) so pushed activity reads
  exactly like `get_change_comments` output the model already knows.
- **golib/e sentinels with structured fields** — for the subscription store, poller, and emission path, matching
  internal/gerritclient conventions.
- **Model-facing prompts drive behavior** — server instructions and tool descriptions teach the agent when to
  subscribe (after pushing a reviewable change, when awaiting a review outcome) and what auto-unsubscription means,
  following the first-contact prompt work.

## Deviations

- **Instructions become composed** — the static `const instructions` (cmd/go-gerrit-mcp/main.go:23) grows a
  conditional review-notifications section emitted only when the feature is enabled. Zero-config instructions stay
  byte-identical to today.
- **First background goroutine** — the poller is the codebase's first concurrent component. It inherits the signal
  context from `run`, logs to stderr only (stdout belongs to the stdio transport), and terminates with the session.
  At its boundary it logs-and-retries poll failures instead of returning them — a background loop has no caller;
  "handle once" is satisfied by logging.
- **Raw notification emission via wrapping transport** — the pinned Go SDK has no public generic notification
  sender, so the channel event is written as an ID-less `jsonrpc.Request` through a transport wrapper that captures
  the `mcp.Connection` (`Write` is documented concurrency-safe). Replace with the SDK's native API when one ships.

## Current State

The server is entirely request-driven: tools fetch from Gerrit on demand, render llmxml, return. No background
work, no per-session state beyond the shared client, no server-initiated messages. An agent that pushed a change
for review learns about review activity only by re-reading the change.

## Desired End State

With the feature enabled, the server declares the `claude/channel` capability, registers subscribe/unsubscribe
tools, and appends channel guidance to its instructions. The agent subscribes to changes it pushed or awaits; a
poller (60s default interval) batches a query over subscribed changes, cursors per change, and turns new activity —
human comments and replies, votes (bare votes included), status transitions — into self-sufficient llmxml payloads
pushed as `notifications/claude/channel` events. Filters drop the caller's own activity (default; flag to include),
excluded accounts, and messages matching operator-configured regexes. Merged or abandoned changes produce a final
notification announcing automatic unsubscription and leave the subscription set. Zero configuration keeps the
server byte-identical to today: no tools, no capability, no instructions, no polling.

## Resolved Questions

- Delivery mechanism → the Claude Code channels contract; self-sufficient payloads; no consumption tool (ADR 2.1).
- Notification scope → explicit per-session subscriptions; no account-wide mode (ADR 2.2).
- Feature gating → dedicated flag family with env mirrors; deliberately not a capability group.
- Bot noise → operator-configured account exclusion list plus content-pattern regexes; no tag-based heuristics.
  Invalid regex → aggregated startup failure.
- Own activity → skipped by default, included via flag.
- Bare human votes → included; often the awaited review outcome.
- Poll interval → 60s default, flag-configurable.
- Terminal states → automatic unsubscription with a final announcing notification.
- Persistence → none; session lifetime; resuming agents re-subscribe.

## Not Yet Specified

- Exact flag, mirror, and tool names — frame-stage, against the glossary.
- Notification payload element design (tag names, meta attributes, how threads/votes/transitions render) —
  frame-stage, reusing the comment-render vocabulary.
- Poller mechanics detail: one batched `change:X OR change:Y` query vs per-change fetches, cursor granularity,
  dedup keys — frame-stage; research confirms both endpoints and per-vote dates exist.
- `ServerOptions.Capabilities` override vs inferred tools capability — needs a learning test during implementation
  (research flags the interaction).
- Research-preview operational caveats surface (README wording for `--dangerously-load-development-channels`) —
  frame-stage.

## Recorded ADRs

- [ADR 2.1: Review notifications ride the Claude Code channels contract](./adr/2.1-channels-contract-delivery.md)
- [ADR 2.2: Session-scoped explicit subscriptions over account-wide watching](./adr/2.2-session-scoped-subscriptions.md)
