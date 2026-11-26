# go-gerrit-mcp

An MCP (Model Context Protocol) server that exposes Gerrit code review operations as capability-gated tools. An AI
agent can search and read changes, publish review comments, vote, and drive change-state transitions. Every write
capability is an explicit operator opt-in.

- Transport: stdio
- Target platform: Gerrit 3.13+, authenticated via HTTP credentials
- Output: llmxml, semantically tagged text meant to be read by a model (see [Output format](#output-format))
- Companion: a Claude Code plugin that brings [review notifications](#review-notifications) into the session

## Safety posture

Two defaults encode it:

- Zero configuration exposes the `read` group only. Write capability never appears unless enabled via `--groups`.
- The own-changes restriction is on by default. Even with write groups enabled, trail-leaving operations (comments,
  votes, state changes, anything other humans see) are refused on changes the authenticated account does not own,
  until the operator explicitly passes `--own-changes-only=false`.

An agent leaving unwanted trail on colleagues' changes is an externally visible failure; a missing capability is a
locally discoverable inconvenience. The defaults are chosen accordingly. Widen deliberately:

```sh
# agent may comment and vote on anyone's changes, but only within two projects
go-gerrit-mcp --groups read,comment,transition --own-changes-only=false --projects core,infra
```

## Install

Binary release: download the binary for your platform from
[GitHub Releases](https://github.com/GaijinEntertainment/go-gerrit-mcp/releases), make it executable, and put it on
your `PATH`:

```sh
# macOS on Apple silicon; substitute <os>_<arch> from: linux, darwin, windows x amd64, arm64
curl -Lo /usr/local/bin/go-gerrit-mcp \
  https://github.com/GaijinEntertainment/go-gerrit-mcp/releases/latest/download/go-gerrit-mcp_darwin_arm64
chmod +x /usr/local/bin/go-gerrit-mcp
```

Docker:

```sh
docker pull ghcr.io/gaijinentertainment/go-gerrit-mcp:latest
```

go install, with Go 1.27.4 or newer. An older Go downloads that toolchain by itself, unless `GOTOOLCHAIN` forbids it:

```sh
go install dev.gaijin.team/go/go-gerrit-mcp/cmd/go-gerrit-mcp@latest
```

## Quick start

The server reads connection identity from environment variables only; credentials never travel through flags:

| Variable          | Meaning                                            |
| ----------------- | -------------------------------------------------- |
| `GERRIT_URL`      | Base URL of the Gerrit instance                    |
| `GERRIT_USERNAME` | Account username for HTTP Basic authentication     |
| `GERRIT_TOKEN`    | HTTP credential paired with the username           |

Generate the credential in Gerrit under **Settings → HTTP Credentials** (an HTTP password, or an auth token on instances
that issue them). All three variables are required. A server that cannot use them does not exit: it stays up and reports
the cause (see [Server readiness](#server-readiness)).

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

Everything the server needs is read from the environment: the identity variables above plus a `GERRIT_MCP_*` mirror
for every flag (see [Configuration reference](#configuration-reference)). MCP server processes inherit the session
environment, so in Claude Code one user-level registration turns into per-project configuration, down to different
Gerrit instances with different credentials per repository.

`~/.claude.json` holds the registration, with no `env` block:

```json
{
  "mcpServers": {
    "gerrit": {
      "command": "go-gerrit-mcp"
    }
  }
}
```

`<project>/.claude/settings.local.json` carries the project's environment; every `env` entry reaches the server
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

Values shared by most projects can sit one layer down in `~/.claude/settings.json`. Settings files merge, with
`.claude/settings.local.json` over `.claude/settings.json` over `~/.claude/settings.json`, so a project declares only
its deltas. Anything no layer sets falls back to the server's own defaults: read-only, own changes.

The registration's `env` block stays empty for a reason. A variable named there shadows every settings layer, and
references are no workaround: `${VAR}` expands only from the shell environment that launched `claude`, never from
settings files, and anything unresolved reaches the server as a literal string.

Other MCP clients inherit their launch environment the same way, so per-directory tooling such as
[direnv](https://direnv.net/) achieves the identical split without client support.

## Capability groups

Capability is selected at startup via `--groups` as a comma-separated list. Groups are independent and combinable,
with no privilege ladder: each write-capable group bundles the minimal change-read subset it needs to work on its
own, and enabled groups union.

| Group        | Tools                                                                                    |
| ------------ | ---------------------------------------------------------------------------------------- |
| `read`       | `search_changes`, `get_change`, `list_change_files`, `get_file_diff`, `get_change_comments`, `get_review_activity` |
| `comment`    | `get_change`, `get_change_comments`, `post_comments`                                     |
| `transition` | `get_change`, `set_vote`, `transition_change`                                            |

`get_server_info` is outside the groups (see [Server readiness](#server-readiness)).

### Tools

- `search_changes`: query changes with Gerrit's change query syntax, paginated.
- `get_change`: one change in review-relevant detail: status, owner, labels with votes, current revision, messages.
- `list_change_files`: files touched by a revision, with per-file change stats.
- `get_file_diff`: the diff of one file in a revision.
- `get_change_comments`: comment threads on a change, with resolution state and comment ids. Returns unresolved
  threads only by default; `status=all` fetches the full history, `status=resolved` the settled threads.
- `get_review_activity`: new review activity on a list of changes past a point that the caller keeps, as JSON for
  a program. The server keeps no state. The [review notifications](#review-notifications) plugin calls it.
- `post_comments`: publish a review in one call: optional top-level message plus inline, range, file-level, and
  reply comments. Replies anchor to comment ids from `get_change_comments`; `resolved` toggles the thread state.
- `set_vote`: set a label vote (e.g. `Code-Review`) with an optional message; value `0` clears an own vote.
- `transition_change`: move a change's state: `submit`, `abandon`, `restore`, `wip`, or `ready`, with an optional
  message (submit accepts none). Gerrit's refusal (a blocked submit, a restore of a merged change) is reported
  verbatim.
- `get_server_info`: which server this is and whether it can serve Gerrit, as JSON for a program: the server name and
  version, the MCP protocol version of the session, the `state` (see [Server readiness](#server-readiness)), the Gerrit
  account, and the enabled groups, tools, and project scope.

## Review notifications

After an agent pushes a change for review, the outcome it waits for comes later: a CI verdict, a reviewer's reply, an
approval. The Claude Code plugin [gerrit-review-notifications](plugins/gerrit-review-notifications/) brings that
activity into the session by itself. The agent subscribes to a change once with `subscribe_change`, and new change
messages, votes, inline comment threads, and status transitions then arrive as `review_activity` blocks. A block uses
the same llmxml vocabulary as the read tools and carries the activity whole, so nothing needs fetching afterwards.

Install the plugin in Claude Code:

```
/plugin install gerrit-review-notifications --marketplace GaijinEntertainment/go-gerrit-mcp
```

The plugin starts no server of its own. It calls `get_review_activity` on the Gerrit MCP server that the session already
runs, so it needs:

- Claude Code 2.1.330 or newer;
- go-gerrit-mcp v1.2.0 or newer, with the `read` group enabled and `get_review_activity` kept by the tool filters;
- that server registered under the name `gerrit`, or the plugin option `server` set to the name it has.

Before a subscription the plugin checks the server through `get_server_info`, and it refuses with the cause when the
server is not ready. Subscriptions belong to one session and leave no trace on the Gerrit instance. The server holds
nothing about them. The [plugin README](plugins/gerrit-review-notifications/README.md) covers delivery, the end of a
subscription, and the limits.

Which activity counts is operator configuration on the server. The server applies no heuristics of its own and filters
nothing by message tag, because a bot's verdict is often exactly the outcome the agent is waiting for. The authenticated
account's own messages, votes, and comments are skipped by default, and the `--activity-*` flags keep them or exclude
accounts and message patterns (see [Configuration reference](#configuration-reference)).

## Configuration reference

Behavior is configured by CLI flags, each mirrored by an environment variable so one configuration style works for
binary and Docker invocations alike. Precedence: the flag wins over its mirror, and the mirror wins over the default.

| Flag                 | Mirror                        | Default | Meaning                                                        |
| -------------------- | ----------------------------- | ------- | -------------------------------------------------------------- |
| `--groups`           | `GERRIT_MCP_GROUPS`           | `read`  | Comma-separated capability groups: `read`, `comment`, `transition` |
| `--projects`         | `GERRIT_MCP_PROJECTS`         | (empty) | Project allowlist confining every operation, reads included    |
| `--own-changes-only` | `GERRIT_MCP_OWN_CHANGES_ONLY` | `true`  | Refuse trail-leaving operations on changes not owned by the authenticated account |
| `--include-tools`    | `GERRIT_MCP_INCLUDE_TOOLS`    | (empty) | Keep only the listed tools from the group-resolved set         |
| `--exclude-tools`    | `GERRIT_MCP_EXCLUDE_TOOLS`    | (empty) | Remove the listed tools from the group-resolved set            |

Three more flags filter the review activity that `get_review_activity` reports:

| Flag                          | Mirror                                 | Default | Meaning                                              |
| ----------------------------- | -------------------------------------- | ------- | ---------------------------------------------------- |
| `--activity-include-own`      | `GERRIT_MCP_ACTIVITY_INCLUDE_OWN`      | `false` | Keep the authenticated account's own messages, votes, and comments |
| `--activity-exclude-accounts` | `GERRIT_MCP_ACTIVITY_EXCLUDE_ACCOUNTS` | (empty) | Usernames or numeric account IDs whose messages, votes, and comments are dropped |
| `--activity-exclude-patterns` | `GERRIT_MCP_ACTIVITY_EXCLUDE_PATTERNS` | (empty) | Regular expressions; a message or comment whose text matches is dropped |

Notes:

- `--own-changes-only` takes an explicit boolean value: `--own-changes-only=false`. A bare flag without a value is a
  configuration error.
- Project scoping (`--projects`) is enforced server-side: a project clause is injected into every change query
  regardless of what the agent composed, and direct operations on out-of-scope changes are refused.
- Tool filters only narrow. `--exclude-tools` removes tools from what the groups resolved; `--include-tools` keeps
  only the listed subset of it. A tool outside the enabled groups can never be activated by a filter, and exclude
  wins over include. Filter entries naming no known tool fail startup, so misconfigurations surface immediately.
- The two exclusion lists are comma-separated. An invalid pattern fails startup with an error naming it. The activity
  filters drop messages, votes, and comments only: a status transition is always reported, and a dropped comment still
  arrives as context when another new comment of its thread is kept.
- Configuration errors are aggregated: the server reports every invalid value at once, then exits non-zero. An unknown
  or malformed flag stops the server before that, with its own error. A Gerrit connection that does not work is not a
  configuration error (see [Server readiness](#server-readiness)).

## Server readiness

The server checks its Gerrit connection once, at startup. `get_server_info` reports the outcome as `state`, with a
`reason` when the state is not `ready`:

| State                  | Meaning                                                                  |
| ---------------------- | ------------------------------------------------------------------------ |
| `ready`                | Gerrit accepted the credentials, and the enabled tools are registered    |
| `not_configured`       | A required variable is unset, or no client can be built from `GERRIT_URL` |
| `credentials_rejected` | Gerrit answered the credential check with 401 or 403                     |
| `unreachable`          | The credential check failed in any other way, such as a network error    |

A server in a state other than `ready` stays up and registers `get_server_info` alone. The tool result, the server
instructions, and stderr name the reason, so the agent can tell the user what to correct. A stdio server that exits
can show only as a closed connection in the client, which loses the reason. The server does not check again: correct the
cause and restart the server.

`get_server_info` is registered in every state. No group selects it and no tool filter removes it.

## Output format

Every tool that a model reads responds in llmxml, an LLM-digestible subset of XML: line-structured, semantically tagged
text. The exceptions are `get_review_activity` and `get_server_info`, which return JSON for the plugin that calls them.
Attributes carry metadata; element bodies carry content:

```
<changes query="status:open owner:self" start="0" count="1" more="false">
<change number="12345" project="core" branch="main" status="NEW" owner="Jane Doe (jdoe)" updated="2026-07-10T20:48:32Z">fix scanner initialization</change>
</changes>
```

There is no XML declaration, no namespaces, and no schema; nothing parses it back. If you need machine-readable
Gerrit data, use Gerrit's REST API directly; this format is for model consumption.

## License

[MIT](LICENSE)
