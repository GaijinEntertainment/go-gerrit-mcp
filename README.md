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

**Binary release** — download the archive for your platform from
[GitHub Releases](https://github.com/GaijinEntertainment/go-gerrit-mcp/releases), unpack, and put `go-gerrit-mcp` on
your `PATH`.

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
