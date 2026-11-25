# gerrit-review-notifications Plugin

A Claude Code mod that delivers review activity on subscribed Gerrit changes into the session. It holds the
subscriptions and the timer; the go-gerrit-mcp server only reports activity through `get_review_activity`. Why delivery
is a mod, and why the server keeps no state: [ADR 2.1](../../design-docs/adr/2.1-notification-delivery.md) and
[ADR 2.2](../../design-docs/adr/2.2-session-scoped-subscriptions.md).

## Components

Run every command in this file from `plugins/`.

- **`hooks/register.tsx`** — the whole implementation, loaded through `modules` in `hooks/hooks.json`.
- **`types/index.d.ts`** — the `$.state` contract: `subscriptions` and `isMainTurnRunning`.
- **`tests/register.test.ts`** — the test-kit suite, on a mocked clock and a stub MCP server. Run
  `claude plugin test gerrit-review-notifications` after any change to the module.
- **`tsconfig.json`** — extends `.claude-plugin/types/tsconfig.json`, which Claude Code generates for its own version
  and gitignores. Start `claude --plugin-dir gerrit-review-notifications` interactively once to generate it, then
  type-check with `npx -p typescript tsc -p gerrit-review-notifications`.

## How It Works

- `subscribe_change` calls `get_review_activity` without `since` and stores the returned `since`, `status`, and `url`
  for the change in `$.state`.
- A `$.clock.every` timer calls `get_review_activity` with the stored `since` and `status` of every subscription, in
  one call. An entry with `error` keeps its stored point; an entry with `ended` removes the subscription.
- One poll makes one delivery. The full content is appended with `$.session.append`. When no turn of the main agent
  runs, `$.prompt.submit` then starts a turn with one line for each change: its number and the `summary` of the entry.
- An answer counts only for the subscription that asked for it. A subscription that ended, or ended and started again,
  while the server answered gets neither the delivery nor the returned point.
- `turn.start` and `turn.complete` of the main agent set `isMainTurnRunning`; a subagent's turns do not count.
- A reload runs `session.start` again, which restarts the timer when subscriptions exist.

## Known Limits

- **The engine tells no hook whether a turn runs** (observed on 2.1.332). The mod tracks it from `turn.start` and
  `turn.complete`, in `$.state` so that a reload during a turn keeps the answer.
- **The row of the submitted prompt cannot be redrawn.** The engine raises no `ui.render` for a plugin's own prompt row
  to that plugin (observed on 2.1.332), so the prompt text itself is the short form.
- **`structuredContent` does not reach `$.mcp.call`** (observed on 2.1.330), so the server returns JSON as text.

## Conventions

- **One vocabulary.** A followed change is a **subscription**, never a watch: `docs/glossary.md` reserves that word for
  Gerrit's own watched-projects setting.
- **The mod never parses `content`.** The model reads the server's `review_activity` block unchanged, and the short
  prompt takes its words from the `summary` field. No text that a reviewer wrote goes into a prompt.
- **Mod API** — write each call in full as `$.namespace.method(...)`, and declare a helper that takes `$` at the top
  level of the module: `claude plugin validate` reads both off the source.
