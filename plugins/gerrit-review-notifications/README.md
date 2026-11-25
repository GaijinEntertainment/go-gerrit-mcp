<div align="center">

# gerrit-review-notifications

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Claude Code 2.1.330+](https://img.shields.io/badge/Claude%20Code-2.1.330%2B%20mod-d97757.svg)](https://claude.com/blog/claude-code-mods)

</div>

Review activity on the Gerrit changes you care about arrives in the Claude Code session by itself: the agent subscribes
to a change once, and new messages, votes, inline comment threads, and status transitions reach it without a poll.

## The Problem

After an agent pushes a change for review, the outcome it waits for comes later: a CI verdict, a reviewer's reply, an
approval. An agent has no way to learn about it except to ask again, so it either polls the read tools in a loop and
spends turns on "nothing new", or it stops and the review sits until a person relays it.

Notifications scoped to the whole account do not fix this. Every session then hears about every change of the account,
including the ones another session works on.

## The Solution

One mod that Claude Code loads itself. It starts no process: it calls the `get_review_activity` tool of the
[go-gerrit-mcp](../../README.md) server that the session already runs.

- **`subscribe_change`** — a tool the agent calls with a change number. A merged or abandoned change is refused.
- **Delivery** — every 60 seconds the mod asks the server about all subscribed changes in one call. New activity is
  appended to the conversation as a note that carries the whole `review_activity` block. When the session is idle, a
  short prompt with one line for each change starts a turn, so the agent reacts without anyone typing. That line is
  the server's own summary, such as `1 message, Code-Review +2`.
- **End of a subscription** — a merged, abandoned, or no longer readable change is reported one last time and then
  dropped. `unsubscribe_change` ends a subscription earlier.
- **On screen** — a band above the prompt lists the subscribed changes as links to Gerrit, and the two tools draw as
  one short row each.

A subscription belongs to one session and leaves no trace on the Gerrit instance.

## Installation

The session needs a go-gerrit-mcp server that offers `get_review_activity`, which its `read` group includes.

```text
/plugin install gerrit-review-notifications --marketplace GaijinEntertainment/go-gerrit-mcp
```

## Configuration

The mod finds the server by the name that `/mcp` lists, `gerrit` by default. For another name, change **Gerrit MCP
server** for `gerrit-review-notifications` in `/config`, or set the `server` option in your user `settings.json`:

```json
{"pluginConfigs": {"gerrit-review-notifications@go-gerrit-mcp": {"options": {"server": "gerrit-work"}}}}
```

Which activity counts is configured on the server, with its `--review-notifications-*` flags: the account's own
activity is skipped by default, and accounts or message patterns can be excluded.

## Limits

- A subscription does not outlive its session: a new session, and a session restarted with `--continue`, starts with
  none.
- The poll interval is fixed at 60 seconds.
- `subscribe_change` takes a change number. For a Change-Id or `project~number`, the agent gets the number from
  `get_change` first.
- The row of the short prompt keeps Claude Code's own drawing: a mod cannot redraw the row of a prompt it submitted.
- A review message is text that somebody else wrote, and the agent reads it. The mod keeps that text out of the prompt
  that starts a turn and delivers it only in the note, which Claude Code marks as coming from a plugin. Exclude accounts
  or patterns on the server when a source of messages is not trusted. An excluded comment still arrives as part of its
  thread when a reply in that thread is not excluded.

## License

MIT
