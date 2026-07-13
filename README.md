# go-gerrit-mcp

An MCP (Model Context Protocol) server exposing Gerrit code review operations as capability-gated tools. It lets an
AI agent search and read changes, publish review comments, vote, and drive change-state transitions — with a safety
posture that makes every write capability an explicit operator opt-in.

- **Transport:** stdio
- **Target platform:** Gerrit 3.13+, authenticated via HTTP credentials
- **Output:** llmxml — semantically tagged text meant to be read by a model (see [Output format](#output-format))

## Safety posture

Two defaults encode it:

- **Zero configuration exposes the `read` group only.** Write capability never appears unless enabled via `--groups`.
- **The own-changes restriction is on by default.** Even with write groups enabled, trail-leaving operations —
  comments, votes, state changes, anything other humans see — are refused on changes the authenticated account does
  not own, until the operator explicitly passes `--own-changes-only=false`.

An agent leaving unwanted trail on colleagues' changes is an externally visible failure; a missing capability is a
locally discoverable inconvenience. The defaults are chosen accordingly. Widen deliberately:

```sh
# agent may comment and vote on anyone's changes, but only within two projects
go-gerrit-mcp --groups read,comment,transition --own-changes-only=false --projects core,infra
```

## Install

**Binary release** — download the binary for your platform from
[GitHub Releases](https://github.com/GaijinEntertainment/go-gerrit-mcp/releases), make it executable, and put it on
your `PATH`:

```sh
# macOS on Apple silicon; substitute <os>_<arch> from: linux, darwin, windows x amd64, arm64
curl -Lo /usr/local/bin/go-gerrit-mcp \
  https://github.com/GaijinEntertainment/go-gerrit-mcp/releases/latest/download/go-gerrit-mcp_darwin_arm64
chmod +x /usr/local/bin/go-gerrit-mcp
```

**Docker:**

```sh
docker pull ghcr.io/gaijinentertainment/go-gerrit-mcp:latest
```

**go install:**

```sh
go install dev.gaijin.team/go/go-gerrit-mcp/cmd/go-gerrit-mcp@latest
```

## Quick start

The server reads connection identity from environment variables only — credentials never travel through flags:

| Variable          | Meaning                                            |
| ----------------- | -------------------------------------------------- |
| `GERRIT_URL`      | Base URL of the Gerrit instance                    |
| `GERRIT_USERNAME` | Account username for HTTP Basic authentication     |
| `GERRIT_TOKEN`    | HTTP credential paired with the username           |

Generate the credential in Gerrit under **Settings → HTTP Credentials** (an HTTP password, or an auth token on
instances that issue them). All three variables are required; the server exits with an error naming the missing ones.

### Claude Code

```sh
claude mcp add gerrit \
  --env GERRIT_URL=https://gerrit.example.com \
  --env GERRIT_USERNAME=your-username \
  --env GERRIT_TOKEN=your-http-credential \
  -- go-gerrit-mcp
```

### Any MCP client (JSON configuration)

```json
{
  "mcpServers": {
    "gerrit": {
      "command": "go-gerrit-mcp",
      "args": ["--groups", "read"],
      "env": {
        "GERRIT_URL": "https://gerrit.example.com",
        "GERRIT_USERNAME": "your-username",
        "GERRIT_TOKEN": "your-http-credential"
      }
    }
  }
}
```

### Docker

```json
{
  "mcpServers": {
    "gerrit": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "GERRIT_URL", "-e", "GERRIT_USERNAME", "-e", "GERRIT_TOKEN",
        "ghcr.io/gaijinentertainment/go-gerrit-mcp:latest",
        "--groups", "read"
      ],
      "env": {
        "GERRIT_URL": "https://gerrit.example.com",
        "GERRIT_USERNAME": "your-username",
        "GERRIT_TOKEN": "your-http-credential"
      }
    }
  }
}
```

## Per-project configuration in Claude Code

Everything the server needs is read from the environment — the identity variables above plus a `GERRIT_MCP_*`
mirror for every flag (see [Configuration reference](#configuration-reference)) — and MCP server processes inherit
the session environment. In Claude Code that turns one user-level registration into per-project configuration,
down to different Gerrit instances with different credentials per repository.

**`~/.claude.json`** — the registration; no `env` block:

```json
{
  "mcpServers": {
    "gerrit": {
      "command": "go-gerrit-mcp"
    }
  }
}
```

**`<project>/.claude/settings.local.json`** — the project's environment; every `env` entry reaches the server
process:

```json
{
  "env": {
    "GERRIT_URL": "https://gerrit.example.com",
    "GERRIT_USERNAME": "your-username",
    "GERRIT_TOKEN": "your-http-credential",
    "GERRIT_MCP_GROUPS": "read,comment",
    "GERRIT_MCP_PROJECTS": "core,infra"
  }
}
```

Values shared by most projects can sit one layer down in `~/.claude/settings.json` — settings files merge with
`.claude/settings.local.json` over `.claude/settings.json` over `~/.claude/settings.json` — so a project declares
only its deltas. Anything no layer sets falls back to the server's own defaults: read-only, own changes.

The registration's `env` block stays empty for a reason: a variable named there shadows every settings layer, and
references are not a way around that — they never resolve from settings files. `${VAR}` expands only from the
shell environment that launched `claude`, and anything unresolved reaches the server as a literal string.

Other MCP clients inherit their launch environment the same way, so per-directory tooling such as
[direnv](https://direnv.net/) achieves the identical split without client support.

## Capability groups

Capability is selected at startup via `--groups` as a comma-separated list. Groups are independent and combinable —
there is no ladder; each write-capable group bundles the minimal change-read subset it needs to function on its own,
and enabled groups union.

| Group        | Tools                                                                                    |
| ------------ | ---------------------------------------------------------------------------------------- |
| `read`       | `search_changes`, `get_change`, `list_change_files`, `get_file_diff`, `get_change_comments` |
| `comment`    | `get_change`, `get_change_comments`, `post_comments`                                     |
| `transition` | `get_change`, `set_vote`, `transition_change`                                            |

### Tools

- `search_changes` — query changes with Gerrit's change query syntax, paginated.
- `get_change` — one change in review-relevant detail: status, owner, labels with votes, current revision, messages.
- `list_change_files` — files touched by a revision, with per-file change stats.
- `get_file_diff` — the diff of one file in a revision.
- `get_change_comments` — comment threads on a change, with resolution state and comment ids.
- `post_comments` — publish a review in one call: optional top-level message plus inline, range, file-level, and
  reply comments; replies anchor to comment ids from `get_change_comments`; `resolved` toggles the thread state.
- `set_vote` — set a label vote (e.g. `Code-Review`) with an optional message; value `0` clears an own vote.
- `transition_change` — move a change's state: `submit`, `abandon`, `restore`, `wip`, or `ready`, with an optional
  message (submit accepts none). Gerrit's refusal — a blocked submit, a restore of a merged change — is reported
  verbatim.

## Review notifications

An opt-in push channel for review activity. The agent subscribes to a change (`subscribe_change`), and from then on
new change messages, votes, inline comment threads, and status transitions arrive in the session by themselves as
`review_activity` blocks — the same llmxml vocabulary the read tools emit, activity carried whole, so nothing needs
fetching afterwards. `unsubscribe_change` ends a subscription early; a merged or abandoned change ends its own with
a final notification saying so, and a change that becomes unreadable (deleted, or no longer visible to the account)
does the same naming the reason.

Subscriptions are per-session and in-memory: they leave no trace on the Gerrit instance, end with the session, and
after a server restart the agent subscribes again. With the feature off — the default — the server is byte-identical
to its pre-feature self: no extra tools, no extra capability, no background polling.

Enabling takes both sides:

1. **Server** — `--review-notifications=true` (or its mirror). The server registers both subscription tools and
   polls Gerrit every `--review-notifications-poll-interval` (default `60s`): one batched query per tick over all
   subscribed changes, detail fetches only for changes that actually moved.
2. **Client** — delivery rides the Claude Code **channels** contract (research preview, Claude Code ≥ 2.1.80).
   For a server registered plainly under `mcpServers`, launch with
   `claude --dangerously-load-development-channels server:<name>`, where `<name>` is the registration key — it
   becomes the `source` attribute of the injected `<channel>` blocks. Allowlisted channel plugins load with
   `claude --channels` instead.

Research-preview caveats: organization policy can disable channels entirely; the flag syntax may change between
Claude Code releases; and a client without channel support silently drops the events — the server then degrades to
exactly its pre-feature behavior, with no errors on either side.

Noise control is operator configuration, not server heuristics — nothing is filtered by message tag, because a CI
verdict is often exactly the awaited outcome:

- the authenticated account's own activity is skipped by default (`--review-notifications-include-own` keeps it);
- `--review-notifications-exclude-accounts` silences accounts by username or numeric ID;
- `--review-notifications-exclude-patterns` drops events whose message or comment text matches a regular
  expression; an invalid pattern fails startup naming it.

Every flag has a `GERRIT_MCP_*` mirror riding the same settings layering as the rest of the configuration (see
[Per-project configuration](#per-project-configuration-in-claude-code)), so a project can enable notifications and
pick its exclusions in its own settings file.

## Configuration reference

Behavior is configured by CLI flags, each mirrored by an environment variable so one configuration style works for
binary and Docker invocations alike. Precedence: **flag wins over its mirror, the mirror wins over the default.**

| Flag                 | Mirror                        | Default | Meaning                                                        |
| -------------------- | ----------------------------- | ------- | -------------------------------------------------------------- |
| `--groups`           | `GERRIT_MCP_GROUPS`           | `read`  | Comma-separated capability groups: `read`, `comment`, `transition` |
| `--projects`         | `GERRIT_MCP_PROJECTS`         | (empty) | Project allowlist confining every operation, reads included    |
| `--own-changes-only` | `GERRIT_MCP_OWN_CHANGES_ONLY` | `true`  | Refuse trail-leaving operations on changes not owned by the authenticated account |
| `--include-tools`    | `GERRIT_MCP_INCLUDE_TOOLS`    | (empty) | Keep only the listed tools from the group-resolved set         |
| `--exclude-tools`    | `GERRIT_MCP_EXCLUDE_TOOLS`    | (empty) | Remove the listed tools from the group-resolved set            |
| `--review-notifications` | `GERRIT_MCP_REVIEW_NOTIFICATIONS` | `false` | Enable [review notifications](#review-notifications) |
| `--review-notifications-poll-interval` | `GERRIT_MCP_REVIEW_NOTIFICATIONS_POLL_INTERVAL` | `60s` | Poll cadence for subscribed changes, as a Go duration |
| `--review-notifications-include-own` | `GERRIT_MCP_REVIEW_NOTIFICATIONS_INCLUDE_OWN` | `false` | Keep the authenticated account's own activity in notifications |
| `--review-notifications-exclude-accounts` | `GERRIT_MCP_REVIEW_NOTIFICATIONS_EXCLUDE_ACCOUNTS` | (empty) | Accounts (usernames or numeric IDs) whose activity never becomes a notification |
| `--review-notifications-exclude-patterns` | `GERRIT_MCP_REVIEW_NOTIFICATIONS_EXCLUDE_PATTERNS` | (empty) | Comma-separated regular expressions; matching message or comment text never becomes a notification |

Notes:

- `--own-changes-only` takes an explicit boolean value: `--own-changes-only=false`. A bare flag without a value is a
  configuration error.
- **Project scoping** (`--projects`) is enforced server-side: a project clause is injected into every change query
  regardless of what the agent composed, and direct operations on out-of-scope changes are refused.
- **Tool filters** only narrow. `--exclude-tools` removes tools from what the groups resolved; `--include-tools`
  keeps only the listed subset of it. A tool outside the enabled groups can never be activated by a filter, and
  exclude wins over include. Filter entries naming no known tool fail startup, so misconfigurations surface
  immediately.
- Configuration errors are aggregated: the server reports every problem at once, then exits non-zero.

## Output format

Every tool responds in **llmxml**: an LLM-digestible subset of XML — line-structured, semantically tagged text meant
to be read by a model. Attributes carry metadata, element bodies carry content:

```
<changes query="status:open owner:self" start="0" count="1" more="false">
<change number="12345" project="core" branch="main" status="NEW" owner="Jane Doe (jdoe)" updated="2026-07-10T20:48:32Z">fix scanner initialization</change>
</changes>
```

There is no XML declaration, no namespaces, and no schema; nothing parses it back. If you need machine-readable
Gerrit data, use Gerrit's REST API directly — this format is for model consumption.

## License

[MIT](LICENSE)
