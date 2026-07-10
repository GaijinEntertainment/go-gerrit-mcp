# Research: go-gerrit-mcp — configurable, publishable Gerrit MCP server

- **Date:** 2026-07-10
- **Teammates:** 5 (2 waves)
- **Scopes investigated:** Gerrit REST API; Gerrit MCP landscape; Go MCP SDKs; MCP server config/distribution
  conventions; go-gerrit client library

## Gerrit REST API surface

Source: official docs at gerrit-review.googlesource.com/Documentation/ (rest-api-changes.html, rest-api.html,
user-search.html). Doc snapshot: v3.14.1-480.

### Findings

**Change-centric read endpoints:**

| Purpose | Endpoint |
|---|---|
| Query changes | `GET /changes/?q=<query>` → [ChangeInfo]; repeatable `q`; `n`, `S`/`start`, `no-limit`; `_more_changes` flag on last item |
| Get change / detail | `GET /changes/{change-id}` and `/detail` |
| Get commit message | `GET /changes/{change-id}/message` |
| Revision info / commit / actions | `GET /changes/{change-id}/revisions/{revision-id}[/commit\|/actions]` |
| List files | `GET .../revisions/{revision-id}/files/` (params `reviewed`, `q`, `parent`, `base` — mutually exclusive) |
| File content / diff / blame | `GET .../files/{file-id}/content\|/diff\|/blame` (diff params: `base`, `parent`, `intraline`, `context`, `whitespace`) |
| Patch | `GET .../revisions/{revision-id}/patch` (base64; `raw`, `zip`, `path`, `parent`, `context` params) |
| Related / submitted-together | `GET .../related`, `GET /changes/{change-id}/submitted_together` |
| Change messages | `GET /changes/{change-id}/messages[/{id}]` |
| Comments (published) | `GET /changes/{change-id}/comments` (map path→[CommentInfo], includes `patch_set`+`author`), `GET .../revisions/{revision-id}/comments/` |
| Drafts (caller's) | `GET /changes/{change-id}/drafts`, `GET .../revisions/{revision-id}/drafts/` |
| Reviewers / votes | `GET /changes/{change-id}/reviewers/`, `.../reviewers/{account-id}/votes/`, `suggest_reviewers?q=`, `GET /changes/{change-id}/attention` |

`o=` option fields (same set for query/get/detail): `LABELS`, `DETAILED_LABELS`, `SUBMIT_REQUIREMENTS`,
`CURRENT_REVISION`, `ALL_REVISIONS`, `CURRENT_COMMIT`, `ALL_COMMITS`, `CURRENT_FILES`, `ALL_FILES`,
`DETAILED_ACCOUNTS`, `REVIEWER_UPDATES`, `MESSAGES`, `CURRENT_ACTIONS`, `CHANGE_ACTIONS`, `REVIEWED`, `SUBMITTABLE`,
`WEB_LINKS`, `COMMIT_FOOTERS`, `TRACKING_IDS`, `STAR`, `PARENTS`, etc. File/commit options require a revision option.

Query operators (user-search.html): `status:`, `is:`, `owner:`, `reviewer:`, `cc:`, `author:`, `commentby:`,
`project:`, `branch:`, `topic:`, `hashtag:`, `label:`, `message:`, `file:`/`path:`, `has:`, `after:`/`before:`/`age:`;
implicit AND, `OR`, `-` negation; `self` = calling user.

The general change message (top-level review comment) is **not** in the comments list — it comes from change
messages / change detail. Magic file paths: `/COMMIT_MSG`, `/MERGE_LIST`, `/PATCHSET_LEVEL`.

**Comment-write endpoints** — two mechanisms:

1. **SetReview**: `POST /changes/{change-id}/revisions/{revision-id}/review` with **ReviewInput** — one call can carry
   `message` (top-level), `comments` (map path→[CommentInput]) publishing inline/file/range comments, `labels` (votes),
   `drafts` handling (`PUBLISH`/`PUBLISH_ALL_REVISIONS`/`KEEP`, default `KEEP`), `draft_ids_to_publish`, `notify`
   (`NONE|OWNER|OWNER_REVIEWERS|ALL`, default `ALL`), `reviewers`, `ready`/`work_in_progress`, attention-set fields,
   `tag`. Fails 409 on a pending change edit.
2. **Draft CRUD**: `PUT /changes/{change-id}/revisions/{revision-id}/drafts` (create),
   `PUT|GET|DELETE .../drafts/{draft-id}`; published later via SetReview.

**CommentInput**: `path`, `side` (`REVISION`|`PARENT`), `line` (1-based; 0/absent = file comment), `range`
(start/end line+char), **`in_reply_to`** (parent comment UUID — how a threaded reply is expressed), `message`,
**`unresolved`** (bool; thread state = the `unresolved` of the chronologically last comment in the thread; on input
defaults to `false` for orphans, or inherits the `in_reply_to` comment's value). Deleting a published comment
requires the Administrate Server capability.

**Vote & lifecycle endpoints:**

| Purpose | Endpoint |
|---|---|
| Set votes | SetReview `labels` map (label→int); auto-adds caller as reviewer |
| Delete vote | `POST /changes/{change-id}/reviewers/{account-id}/votes/{label-id}/delete` (DELETE-with-body deprecated); 409 if change merged |
| Add/delete reviewer | `POST /changes/{change-id}/reviewers` (ReviewerInput: `reviewer`, `state` REVIEWER\|CC), `POST .../reviewers/{account-id}/delete` |
| Submit | `POST /changes/{change-id}/submit` (may submit whole topic; 409 "blocked by <label>" when submit rules fail) |
| Abandon / restore | `POST /changes/{change-id}/abandon\|/restore` |
| Rebase | `POST /changes/{change-id}/rebase` (+ revision-level, + `rebase:chain`) |
| Revert | `POST /changes/{change-id}/revert`, `/revert_submission` |
| Move / cherry-pick | `POST /changes/{change-id}/move`, `POST .../revisions/{revision-id}/cherrypick` |
| WIP / ready | `POST /changes/{change-id}/wip\|/ready`; also inline via ReviewInput (`ready` XOR `work_in_progress`) |
| Topic / hashtags | `PUT|DELETE /changes/{change-id}/topic`, `POST /changes/{change-id}/hashtags` |
| Attention set | `POST /changes/{change-id}/attention`, `POST .../attention/{account-id}/delete` |
| Delete change | `DELETE /changes/{change-id}` (owner with Delete Own Changes on NEW/ABANDONED, else admin) |

**Data dependencies (write flow → identifiers → supplying read endpoint):**

- `{change-id}`: `<project>~<changeNumber>` recommended; from ChangeInfo `id`/`project`/`_number` (query/get).
- `{revision-id}`: `current` | SHA | patch number; from ChangeInfo `current_revision`/`revisions{}` (needs
  `CURRENT_REVISION`/`ALL_REVISIONS`).
- Reply to comment: parent comment UUID (`in_reply_to`) + revision + path (+line/range) — from list-comments
  (CommentInfo `id`, `path`, `line`, `patch_set`).
- Draft update/delete/publish: draft UUID — from list-drafts / create-draft response.
- Set/delete vote: account-id + label name — from Get Change Detail (`labels{}`, `permitted_labels`,
  `removable_reviewers`; needs `DETAILED_LABELS`/`DETAILED_ACCOUNTS`) or List Reviewers.
- Submit/rebase availability: Get Revision Actions or `CURRENT_ACTIONS`/`SUBMITTABLE`/`SUBMIT_REQUIREMENTS` options.

**Authentication:**

- Default anonymous; **`/a/` path prefix forces HTTP Basic auth** with the account's generated HTTP credential;
  bypasses XSRF tokens.
- OAuth bearer tokens usable as the Basic-auth password; cookie auth via `access_token` query param also documented.
- **XSSI protection: every JSON response starts with `)]}'` on its own line** — must be stripped before parsing.
- Credential issued at account Settings → HTTP Credentials; `gitBasicAuthPolicy` selects generated-token vs LDAP.
- Timestamps UTC `yyyy-mm-dd hh:mm:ss.fffffffff`; documented response codes 400/403/404/405/409/412/422/429.

**Version compatibility (3.x):**

- **Gerrit 3.13 deprecated long-lived HTTP passwords for time-limited authentication tokens**
  (`MigratePasswordsToTokens` creates a `legacy` token). REST mechanics unchanged (`/a/` + Basic); only issuance
  changed.
- Policy: API extended additively; clients must ignore unknown fields; incompatible removals rare, announced in
  release notes.
- Newer 3.x additions that may be absent on older servers: submit requirements (`SUBMIT_REQUIREMENTS`), flows
  endpoints, `patch:apply`, fix suggestions, ported comments/drafts, `PARENTS`/`STAR` options. Assignee concept
  removed in favor of attention set across 3.x. DELETE-with-body variants deprecated in favor of `POST .../delete`.

### Open gaps

- Exact per-version endpoint availability is not enumerated in the docs — a given server's own
  `/Documentation/rest-api-changes.html` reflects its version.

## Gerrit MCP landscape

### Findings

**Existing Gerrit MCP servers** (GitHub + MCP registries, 2026-07):

| Server | Lang | Tools | Activity | Distribution |
|---|---|---|---|---|
| GerritCodeReview/gerrit-mcp-server (official, Gerrit/Google, Gemini-targeted) | Python | ~20: 11 read (query/detail/files/diff/comments/...) + 9 write (post_review_comment, add_reviewer, abandon, revert, WIP/ready, create_change, set_topic) | 18★, push 2026-07-06, active | source; config via `gerrit_config.json` (multi-host); **shells out to `curl`**; auth git_cookies/http_basic |
| cayirtepeomer/gerrit-code-review-mcp | Python | 3: fetch change, patchset diff, submit review (votes+comments) | 37★, push 2025-11-14 | Smithery/pip; env config; HTTP digest |
| siarhei-belavus/gerrit-mcp | Python | 8 read + draft comment + set_review (votes) | 8★, **stale since 2025-04** | pipx |
| a1loy/gerrit-mcp — **only prior Go implementation** | Go (mark3labs/mcp-go v0.43) | 3, read-only | 0★, push 2025-11-30 | source; Streamable HTTP + SSE |
| LokeshBolisetty/GerritMCPServer | Python | 4 (drafts + publish, no votes) | 2★ | source |
| Long tail (~12 more) | mostly Python | — | 0-1★, single-author | source |

Cross-cutting facts: ~90% Python; majority stdio; **zero Gerrit MCP servers ship release binaries** (pip/pipx/
Smithery/source only); only three post votes. The official server is the only actively-maintained feature-complete
one; it is Python, curl-based, config-file-driven.

**Go MCP SDKs:**

- **modelcontextprotocol/go-sdk (official, maintained with Google)** — 4,785★; **stable v1.6.1 (2026-05-22) with a
  v1.0 no-breaking-changes guarantee**; transports stdio + Streamable HTTP (docs call streamable HTTP the preferred
  networked transport); protocol revisions through 2026-07-28 with back-compat to 2024-11-05; input schemas
  auto-derived from Go structs via `jsonschema` tags.
- **mark3labs/mcp-go (community)** — 8,883★, very active, **pre-v1 (v0.56.0), no stability guarantee**; stdio, SSE,
  Streamable HTTP, in-process; fluent builder or raw-schema tool registration.
- No other Go MCP framework with traction (`golang.org/x/tools/internal/mcp` is internal to gopls).

## MCP server config & distribution conventions

### Findings

Surveyed: github/github-mcp-server (Go, official), GitLab (built-in + zereight/gitlab-mcp), Atlassian Rovo (hosted),
korotovsky/slack-mcp-server (Go), getsentry/sentry-mcp, makenotion/notion-mcp-server.

**Config surface:** credentials travel almost universally through **env vars** (`<VENDOR>_..._TOKEN` shape), not
flags (process-listing leak) and not files. Behavior is exposed as **CLI flags with env-var equivalents** so one
config works for binary and Docker invocations. Documented precedence is inconsistent across the ecosystem:
github-mcp-server has env override flags; Notion has flags override env.

**Capability gating** — four recurring mechanisms, often combined:

1. **Named toolset groups** via comma-list flag/env — `--toolsets`/`GITHUB_TOOLSETS` (~23 groups; special values
   `all`, `default`), `GITLAB_TOOLSETS`, Sentry "skills" (`--disable-skills`). Dominant pattern.
2. **Read-only / permission mode** — github `--read-only`; GitLab `GITLAB_PERMISSION_MODE=readonly|modify|full`;
   Slack write tools off by default, opt-in per env var with allow/deny syntax (`true`, comma-list, `!` prefix).
3. **Fine-grained allow/deny** — github `--tools`; `GITLAB_TOOLS` (allow) + `GITLAB_DENIED_TOOLS_REGEX` (deny);
   complemented client-side by MCP clients' own tool disable lists.
4. **Dynamic toolset discovery** — github `--dynamic-toolsets` (beta): starts with ~4 meta-tools, host enables
   groups at runtime; motivated explicitly by tool-count degrading model performance.

**Tool naming:** snake_case verb_noun is the strong majority (github, GitLab, Slack, Sentry); resource-prefix
namespacing common (`conversations_history`, `issue_read`); Notion is the kebab-case outlier. Tool counts range
18-178+. Anthropic guidance (anthropic.com/engineering/writing-tools-for-agents): "more tools don't always lead to
better outcomes"; consolidate overlapping tools; namespace by service then resource.

**Transport:** the MCP spec (2025-06-18) defines exactly two standard transports — stdio and Streamable HTTP —
and clients "SHOULD support stdio whenever possible". HTTP+SSE (2024-11-05) is deprecated. Observed: local/self-
hosted binaries default to stdio; vendor-hosted remotes use Streamable HTTP + OAuth; stdio-only clients bridge via
`mcp-remote`.

**Distribution (Go servers):** Docker/OCI images near-universal (`ghcr.io/...`); prebuilt binary GitHub Releases
(Slack); npm wrappers even for Go binaries; `go install`/homebrew not prominent. **Official MCP Registry** expects
`server.json` (schema 2025-12-11) with reverse-DNS name (`io.github.<owner>/<name>`, ownership proved via GitHub
login or DNS), `packages[]` with `registryType` ∈ npm/pypi/nuget/oci/mcpb, published via `mcp-publisher` CLI.

## go-gerrit client library (`github.com/andygrunwald/go-gerrit`)

### Findings

**Maintenance:** latest release v1.1.1 (2025-12-05); master 1 commit ahead (adds `WithRunAs`, 2026-03-16). Alive but
slow: ~1.2 commits/month in 2025-2026, 15 open issues + 14 open PRs, MIT, 106★. `go.mod` declares `go 1.16`; single
dep (`google/go-querystring`). README states it was implemented/tested against Gerrit 2.11.3; types extended
piecemeal since — systematic drift acknowledged in issue #213.

**Endpoint coverage** (all on `client.Changes`, all take `ctx`):

- Changes: `QueryChanges` (changes.go:640), `GetChange`/`GetChangeDetail` (:666/:678) with `o=` via
  `ChangeOptions.AdditionalFields`, `ListFiles`, `GetDiff`, `GetPatch`, `GetContent` — all present.
- Comments: `ListChangeComments` (changes.go:758), `ListRevisionComments`, draft CRUD (`CreateDraft`, `UpdateDraft`,
  `DeleteDraft`, `GetDraft`, list variants), `SetReview` (changes_revision.go:386) — all present.
- Votes/reviewers: `AddReviewer`, `DeleteReviewer`, `DeleteVote`, `ListReviewers`, `SuggestReviewers`, `ListVotes` —
  all present.
- Lifecycle: `SubmitChange`, `AbandonChange`, `RestoreChange`, `RebaseChange`, `RevertChange`, `MoveChange`,
  `CherryPickRevision`, `SetTopic`, `SetReadyForReview` — present. **Gaps:** no dedicated set-WIP method (only via
  `ReviewInput.WorkInProgress`); attention set has only `RemoveAttention` (add only via
  `ReviewInput.AddToAttentionSet`).

**Protocol handling:** XSSI `)]}'` stripping built-in (`RemoveMagicPrefixLine`, gerrit.go:496); automatic `/a/`
prefix when authenticated (gerrit.go:363); auth Basic/Cookie/Digest (no built-in bearer type); pagination manual
(`MoreChanges` surfaced, no auto-paging); **errors discard the response body** — `CheckResponse` (gerrit.go:518)
returns only the status line, losing Gerrit's error message (issue #64); `*Response` returned alongside for manual
body reads.

**Type completeness:** `ReviewInput` (changes.go:269) has Labels, Comments map, Drafts policy, Notify, Ready,
WorkInProgress, attention-set fields; **missing `notify_details`**. `CommentInput` (changes.go:310) has `InReplyTo`,
`Range`, `Unresolved *bool` (pointer, distinguishes unset); missing minor `parent` field. Known gaps tracked in
issues #213, #183, #179, #167; API-shape quirk: collections returned as `*[]T`/`*map` (issue #53).

**Alternatives:** go-gerrit is the de-facto standard general-purpose Go client; the only production-hardened
alternative is `go.chromium.org/luci/common/api/gerrit` (proto/pRPC-based, tightly coupled to LUCI, heavyweight).

## Cross-cutting observations

- Every write flow's identifier needs (revision-id, comment UUID, label names) are supplied by a small set of
  change-read endpoints — get change (with options), list comments, list drafts, get change detail — consistent
  with capability groups bundling a minimal read subset.
- The discovery-stage assumption "no viable Gerrit MCP exists" is partially contradicted: an official, actively
  maintained Python server exists (GerritCodeReview/gerrit-mcp-server). Uncontradicted: no Go implementation with
  traction, none with capability gating, none shipping binaries.

## Unresolved questions

- The version of the operator's target Gerrit instance — determines HTTP-password vs auth-token issuance (3.13
  boundary) and availability of newer endpoints. Resolved during alignment: 3.13+ is the target platform.
