# Tasks: Review notifications

- **Date:** 2026-07-13
- **Frame:** [02-review-notifications.frame.md](./02-review-notifications.frame.md)
- **Tracker:** GitHub Issues (this repository) — tasks map 1:1 to issues #49–#63; phases are the
  `Notifications 1–4` milestones; HITL tasks carry the `hitl` label; dependencies are stated in issue bodies

## Phase 1: Tracer Bullet — subscribe, poll, push

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| #49 | Learning tests: SDK capability override, raw write | ~2h | — | learning tests passing | AFK |
| #50 | Enable and poll-interval configuration | ~1h | — | config tests | AFK |
| #51 | Subscription store and polling loop | ~3h | #50 | notifications pkg + tests | AFK |
| #52 | Wrapping transport and channel emission seam | ~2h | #49 | emitter seen by stub client | AFK |
| #53 | subscribe_change tool and conditional channel wiring | ~2h | #50 #51 #52 | gated tool + goldens | AFK |
| #54 | Verify tracer bullet live against Claude Code | ~1h | #53 | `<channel>` block in session | HITL |

## Phase 2: Activity deltas and terminal states

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| #55 | Per-kind activity deltas with cursors | ~4h | #51 | delta extraction + tests | AFK |
| #56 | unsubscribe_change and self-sufficient payloads | ~3h | #53 #55 | payload renderer + goldens | AFK |
| #57 | Automatic unsubscription on terminal states | ~2h | #55 #56 | terminal handling + tests | AFK |
| #58 | Verify full notification sequence live | ~1h | #57 | lifecycle ending in auto-unsub | HITL |

## Phase 3: Filters and model-facing prompts

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| #59 | Filter config: include-own, account/pattern exclusions | ~2h | #50 | config tests incl. bad regex | AFK |
| #60 | Filter chain over extracted activity | ~2h | #55 #59 | filter chain + table tests | AFK |
| #61 | Finalize model-facing prompts for subscriptions | ~2h | #56 #57 | instruction goldens on/off | AFK |

## Phase 4: Hardening and operator docs

| # | Task | Est. | Deps | Artifact | Mode |
| --- | --- | --- | --- | --- | --- |
| #62 | Harden poller: failures, lost access, shutdown | ~3h | #57 #60 | failure tests, `-race` clean | AFK |
| #63 | Document review notifications in the README | ~1h | #61 #62 | README section | AFK |

Full descriptions live in the issues; each carries context, work items, acceptance criteria, estimate, artifact, and
references back to the frame, alignment, and ADRs. The issues are the source of truth once picked up; this document
is the cross-session map.

## Dependency Graph

```
Phase 1
  [#49 learning tests]──────────────┐
  [#50 config flags]──┬──────────┐  │
                      ↓          │  │
  [#51 store+poller]──┤          │  │
                      │   [#52 transport]←┘
                      ↓          ↓
  [#53 subscribe tool + wiring]←─┘
                      ↓
  [#54 live tracer gate (HITL)]

Phase 2                          Phase 3 (config side may start after #50)
  [#55 deltas]←──#51               [#59 filter flags]←──#50
      ↓                                 ↓
  [#56 unsubscribe+payloads]←#53   [#60 filter chain]←──#55
      ↓
  [#57 terminal states]────────→  [#61 prompts]
      ↓
  [#58 live sequence gate (HITL)]

Phase 4
  [#62 hardening]←── #57 #60
  [#63 README]←── #61 #62
```
