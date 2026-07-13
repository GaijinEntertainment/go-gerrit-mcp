# Research: Review notifications

- **Date:** 2026-07-13
- **Teammates:** 3 codebase researchers; external facts gathered by the lead (subagent budget exhausted mid-wave)
- **Scopes investigated:** config & registry, gerritclient, tools & llmxml, MCP Go SDK & spec, Gerrit REST API

## Config & registry

### Files investigated

- `internal/config/config.go` — flag/mirror resolution
- `internal/registry/registry.go` — group-to-tool resolution
- `cmd/go-gerrit-mcp/main.go` — server assembly and lifecycle

### Findings

**Flag/mirror resolution.** Every behavior option is a `behaviorFlag` (config.go:76-83): CLI name, `GERRIT_MCP_*`
mirror, usage, fallback. All flags are strings; `resolveFlags` (config.go:163-191) parses CLI args, then fills
non-explicit flags from the mirror, then the fallback. Precedence: flag > mirror > default. Booleans are parsed
with `strconv.ParseBool` after trimming (config.go:137-145). Configuration errors aggregate via `errors.Join`
(config.go:154-156) — every problem reported at once; only malformed CLI args short-circuit. Identity
(`GERRIT_URL`/`GERRIT_USERNAME`/`GERRIT_TOKEN`) is env-only, validated by `missingEnv` (config.go:193-207).

**Group resolution and registration.** `groupTools()` (registry.go:23-47) maps the three capability groups to tool
names; `Resolve` (registry.go:53-85) unions enabled groups, applies include/exclude filters (narrowing only), and
fails startup on unknown filter names. `tools.All(client)` returns every `Tool{Name, Register}` in declaration
order; main.go:77-81 calls `Register(srv)` only for enabled names — disabled tools are never constructed into the
server.

**Instructions.** A single static `const instructions` (main.go:23-33) passed via
`mcp.ServerOptions{Instructions:}` (main.go:61-64). No builder, no per-configuration variation of the prose; only
the tool set varies.

**Lifecycle.** `signal.NotifyContext` for SIGINT/SIGTERM (main.go:48); sequential setup (config → client → server
→ middleware → registry → registration); one startup log line to stderr; `srv.Run(ctx, &mcp.StdioTransport{})`
blocks for the whole server lifetime (main.go:89). No background goroutines exist today. All logging goes to
stderr; stdout belongs to the stdio transport.

## gerritclient

### Files investigated

- `internal/gerritclient/gerritclient.go`, `changes.go`, `review.go`, `transition.go`
- `github.com/andygrunwald/go-gerrit@v1.1.1` types (module cache)

### Findings

**Client state.** `Client{gerrit, self, projects, allowForeign}` (gerritclient.go:38-47); `New` validates
credentials via `GetAccount(ctx, "self")` and caches the result — `Self()` returns the authenticated account
(gerritclient.go:78). 30s HTTP timeout.

**Scoping enforcement.** Project scoping: `scopedQuery` injects the allowlist into every query (changes.go:136);
`checkProjectScope` (changes.go:159-186) gates direct reads, resolving bare identifiers with one fetch;
`GetChange` checks post-fetch. Write scoping (`checkWriteScope`, review.go:25-56) additionally compares
`info.Owner.AccountID` against `c.self.AccountID` unless foreign changes are allowed.

**Read operations.** `GetChange` requests `DETAILED_LABELS, DETAILED_ACCOUNTS, CURRENT_REVISION, MESSAGES,
SUBMITTABLE` (changes.go:41-49) — populates per-voter `LabelInfo.All []ApprovalInfo`, `Messages
[]ChangeMessageInfo`, `CurrentRevision`, `Submittable`. `QueryChanges` requests only `DETAILED_ACCOUNTS`
(changes.go:203) — no messages, no labels in query results. `ListChangeComments`/`ListChangeDrafts` take no
options; drafts carry no author (always the caller). Pagination via the last element's `MoreChanges`.

**Library types relevant to activity detection** (go-gerrit v1.1.1):

- `ChangeInfo.Updated Timestamp` — always present (changes.go:449 in the library).
- `ChangeMessageInfo{ID, Author, Date Timestamp, Message, Tag string, RevisionNumber int}` (library
  changes.go:93-100) — `Tag` carries the message-source tag; `RevisionNumber` maps to `_revision_number`.
- `ApprovalInfo{AccountInfo, Value int, Date string}` (library changes.go:62-66) — each vote carries a date;
  note the field is a plain string, not `Timestamp`, in this library.
- `CommentInfo{PatchSet, Path, Line, Range, InReplyTo, Message, Updated *Timestamp, Author, Unresolved *bool,
  ChangeMessageID, CommitID}` (library changes.go:544-558) — `ChangeMessageID` links a comment to the change
  message it was posted with.

**Error handling.** Per-operation sentinels wrapped with `golib/e` fields; `apiError` attaches HTTP status and the
Gerrit message body (4KiB cap); enrichment helpers (`patchSetFields`, `scopeError`) attach recovery data on the
failure path only. Errors are returned, never logged, inside the package.

## Tools & llmxml

### Files investigated

- `internal/tools/tools.go`, `get-change.go`, `get-change-comments.go`, `search-changes.go`, `error-middleware.go`
- `internal/llmxml/llmxml.go`

### Findings

**Tool pattern.** `Tool{Name string, Register func(*mcp.Server)}` (tools.go:25-28); constructors close over the
shared client; handlers return text-only results via `textResult` (tools.go:46-48); input structs carry dense
`jsonschema:"..."` descriptions; names are centralized constants (tools.go:13-22). `WrapErrors` middleware
(error-middleware.go:17-47) re-wraps every in-band tool error as `<error tool="...">` llmxml.

**Comment-thread pipeline.** `flattenComments` → `buildThreads` (bug-compatible replica of polygerrit's
`createCommentThreads`; get-change-comments.go:147-201) → `renderComments` (unresolved section first;
get-change-comments.go:385-422). Threads group by UI sort order, resolve state comes from the last member in UI
order, display order is chronological. Rendered elements: `<comments change= filter= threads= drafts=>`,
`<unresolved count=>`/`<resolved count=>`, `<file path=>`, `<thread resolved=>`, `<comment id= author= draft=
date= patch_set= line=|lines= in_reply_to=>`.

**Shared helpers.** True cross-file primitives: `accountLabel` (get-change.go:176-187) and `timestamp`
(get-change.go:189-191). `renderLabels`/`renderReviewers`/`renderMessages` are local to get-change.go; comment
rendering helpers are local to get-change-comments.go.

**llmxml.** Builder API: `NewElement(tag, attrs...)`, `.Attr`, `.InlineText`/`.WrapText` (mutually exclusive,
sealed), `String()`. Attribute values are escaped via `strconv.Quote`; body text is embedded verbatim
(llmxml.go:115). Composition is manual string joining with `\n`.

## MCP Go SDK & spec

### Findings

**Pinned SDK:** `github.com/modelcontextprotocol/go-sdk v1.6.1` (go.mod).

**Server-to-client primitives** (from `go doc`):

- `ServerSession.Log(ctx, *LoggingMessageParams) error` — spec `notifications/message`; params carry `Data any`,
  `Level LoggingLevel`, optional `Logger` name.
- `ServerSession.NotifyProgress(ctx, *ProgressNotificationParams) error` — spec `notifications/progress`;
  progress is tied to a request-provided progress token.
- `Server.ResourceUpdated(ctx, *ResourceUpdatedNotificationParams) error` — spec
  `notifications/resources/updated`, delivered only to sessions that subscribed to the resource
  (`resources/subscribe`).
- `ServerSession.Elicit` and `ServerSession.CreateMessage`/`CreateMessageWithTools` — server-initiated *requests*
  (elicitation, sampling), not notifications.
- `Server.Sessions() iter.Seq[*ServerSession]` — enumerates live sessions; a stdio server has exactly one.

**Spec side (2025-06-18).** Server-initiated notifications defined: logging messages, progress, resource
updated/list-changed, tools/prompts list-changed. Resource update delivery requires the client to subscribe
explicitly.

**Claude Code channels contract** (research preview, Claude Code >= v2.1.80; docs:
code.claude.com/docs/en/channels-reference):

- A channel is an MCP stdio server that declares `capabilities.experimental["claude/channel"] = {}` at initialize;
  the presence of the key registers a notification listener in Claude Code.
- Events are pushed by emitting the notification method `notifications/claude/channel` with params
  `{content: string, meta?: Record<string,string>}`. The event reaches the model as
  `<channel source="<server-name>" k="v">content</channel>` — `content` becomes the tag body, each `meta` entry an
  attribute (keys restricted to letters/digits/underscores; others silently dropped), `source` set automatically
  from the server name.
- Server `instructions` are the documented place to teach the model what the events mean and whether to reply.
- Notifications are unacknowledged; if the session did not load the server as a channel (or org policy blocks
  channels), events are dropped silently. Queued events deliver together on the next turn.
- Enablement is per session: `claude --channels <entry>`; during the research preview only allowlisted plugins
  register, and a bare `.mcp.json` server requires `claude --dangerously-load-development-channels server:<name>`.
  Org policy `channelsEnabled` gates everything. The flag syntax and protocol contract may change during the
  preview.
- Optional extensions: standard tools alongside the channel capability (two-way channels), and
  `claude/channel/permission` for permission relay.

**SDK support for the channels contract** (verified against v1.6.1 and v1.7.0-pre.2):

- `ServerCapabilities.Experimental map[string]any` exists and `ServerOptions.Capabilities` lets the server set it.
  Caveat noted in the field docs: setting `Capabilities` overrides the `{"logging":{}}` default and interacts with
  capability inference — verify the tools capability survives when overriding.
- No public generic notification-sending API exists in either version — only typed senders (`Log`,
  `NotifyProgress`, `ResourceUpdated`); the internal `handleNotify` is unexported.
- The emission seam that works on the pinned SDK: the caller owns the `Transport` passed to `Server.Run`
  (main.go:89), the `jsonrpc` subpackage is public "for use by mcp transport authors", and
  `mcp.Connection.Write` is documented as safe for concurrent calls — a wrapping transport can capture the
  connection and write an ID-less `jsonrpc.Request` (a notification) with a custom method concurrently with SDK
  traffic.

### Open gaps

- What mainstream MCP clients surface to the model mid-session from logging/resource-updated notifications is not
  documented. For Claude Code specifically the channels contract (above) is the documented model-visible push
  path.
- The interaction between `ServerOptions.Capabilities` overrides and inferred capabilities (tools) needs a
  verification test during implementation.

## Gerrit REST API (3.13)

### Findings

- **`ChangeInfo.updated`** moves on effectively every mutation (comments, votes, status transitions, topic edits,
  rebases, commit-message edits, …). Timestamp format `2013-02-21 11:16:36.775000000` — nanosecond-formatted,
  sub-second precision available.
- **`ChangeMessageInfo`** documented fields match the library types (id, author, date, message,
  `_revision_number`); the `tag` field exists on the wire (confirmed by library type and ReviewInput's documented
  `tag` mechanism with the `autogenerated:` prefix convention). A bare vote produces a change message ("Patch Set
  N: Code-Review+2") alongside the label update.
- **Votes with `DETAILED_LABELS`**: each `ApprovalInfo` in `LabelInfo.all` carries a `date` — individual vote
  recency is observable without diffing messages.
- **Query syntax**: `change:` accepts a change number or Change-Id; `OR` combinations like
  `change:123 OR change:456` are valid; no documented limit on query length or term count. `before:`/`after:`
  accept `2006-01-02[ 15:04:05[.890][ -0700]]` (UTC default) — second-or-finer granularity server-side filtering
  exists at the query level. `status:merged` / `status:abandoned` detect terminal states.
- **Comments endpoints**: `CommentInfo.updated` is present per comment; there is no documented server-side
  time-window filter on the comments endpoints — recency filtering happens client-side.

### Open gaps

- Whether the default (no `o=`) change query result always includes `updated` — the library type has no omitempty
  on it, and observed responses populate it, but the doc extraction did not state it explicitly. Verify live.
- Exact tagging behavior of specific bots (which messages carry which `tag` values) is instance-specific; verify
  against the target instance during implementation.

## Cross-cutting observations

- The server currently has zero background goroutines; every action is request-driven. Any poller is the first
  concurrent component and must respect the stderr-only logging rule and the ctx-cancellation lifecycle
  (main.go:48-94).
- All model-facing output flows through llmxml renderers in `internal/tools`; comment/message/vote rendering
  helpers already exist and are reusable as-is or by extraction.
- The registration mechanism gates tools by name at startup; instructions prose is currently static — any
  conditional instructions require making the const a composed string.
