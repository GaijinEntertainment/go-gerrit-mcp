# Brief: Review notifications

- **Date:** 2026-07-13
- **Initiative:** 02-review-notifications

## Motivation

An agent that pushes a change for review has no way to learn about review activity except manually re-reading the
change, and an earlier account-wide poller (outside this project) proved the naive approach wrong twice: it notified
about every change the account owned — so concurrent agent sessions woke each other up — and it had no activity
filtering, so CI votes produced the same wake-ups as human review. The server should track review activity for
exactly the changes a session subscribed to and push it to that session alone.

## Goals

- Two tools — subscribe and unsubscribe by change identifier — registered only when the review-notifications flag is
  enabled; model-facing prompts (server instructions, tool descriptions) teach the agent to subscribe after pushing a
  reviewable change or whenever it awaits a review outcome.
- Self-sufficient notifications: after subscribing, the agent starts receiving review activity for that change as
  pushed notifications whose payload carries the new activity inline (rendered llmxml — comment threads, review
  messages, votes, status transitions). No consumption tool; nothing to fetch afterwards.
- Per-instance, in-memory subscription set and cursor. Session scoping falls out of the stdio process model: one
  server per agent session, so concurrent sessions never see each other's subscriptions. No persistence; a restarted
  session re-subscribes.
- Polling-based detection: the server polls Gerrit for subscribed changes on an interval; an empty subscription set
  polls nothing.
- Event filtering:
  - Events authored by the authenticated account are skipped by default; a flag includes them.
  - A configurable account exclusion list (usernames/IDs) silences unwanted accounts, e.g. noisy bots. No tag-based
    bot heuristics: whether a bot is noise or signal (a CI verdict) is the operator's call, and per-user defaults
    belong in the operator's shared client configuration, not in server heuristics.
  - A configurable content-pattern exclusion list drops individual messages and comments by their text, regardless
    of author. Account exclusion is too coarse for accounts that mix signal and noise: an AI reviewer's verdict
    matters while its "review started" announcement does not, and a human comment that merely summons a bot is
    noise from an account that otherwise matters.
  - Human votes without comment text are included — a bare vote is often the awaited review outcome.
- Automatic unsubscription on terminal states: when a subscribed change reaches merged or abandoned, the review is
  almost certainly over — the final notification carries the transition, tells the model the subscription ended
  automatically and no further notifications will arrive, and the poller drops the change.
- Dedicated flags with environment mirrors, following the existing flag/mirror pattern: enable flag, account
  exclusion list, content-pattern exclusion list, include-own toggle, poll interval. This is deliberately not a
  capability group.

## Non-goals

- Notification channel configuration (multiple channels, per-channel settings) — dedicated follow-up work.
- Persistence of subscriptions or cursors across sessions.
- Attention-set integration, SSH stream-events, webhooks — alternative sources deliberately rejected for now.
- Notifying about changes the account owns but the session never subscribed to.

## Constraints

- Zero configuration means the feature does not exist: no tools registered, no instructions emitted (ADR 1.2 safety
  posture: defaults are contractual).
- Subscribing is trail-free — pure local state, nothing visible on the Gerrit instance.
- Project scoping continues to confine every operation, subscriptions included.
- Notification payloads are llmxml (ADR 1.3).

## Key risks

- Gerrit's `updated` timestamp moves on any mutation, so detail fetches may be triggered by events the filters then
  discard — a cost concern, not a correctness one.
- A dead session cannot unsubscribe; the poller must terminate subscriptions meaningfully when a change reaches a
  terminal state (merged/abandoned) instead of polling forever.
- Push delivery depends on what MCP clients actually surface to the model mid-session; payload design must not
  assume more than the protocol guarantees.

## Flagged term ambiguities

- "Review notification channel" — the user-facing name of the feature and its flag family; needs a canonical
  glossary entry distinguishing it from capability groups.
- "Subscription" — the per-session tracked set; needs a definition that pins its session lifetime and trail-free
  nature.

## Questions for research

- How MCP server-to-client push works in the Go SDK (notification types available to a stdio server) and what
  mainstream clients surface to the model mid-session.
- How to detect new votes and distinguish vote-only updates: `ChangeMessageInfo` tags/epochs vs detailed labels.
- Whether comment deltas are derivable from change messages alone or require comment-list diffing per poll.
- Batched change query behavior and limits for `change:X OR change:Y` polling.
- What exactly moves a change's `updated` field, and its timestamp granularity.
