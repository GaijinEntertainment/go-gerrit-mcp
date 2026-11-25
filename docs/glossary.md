# go-gerrit-mcp Glossary

Project vocabulary. Only entries with a real risk of being mis-named are listed — everything else lives in code
documentation. For each term, **Avoid** lists the synonyms that are realistic naming traps: names a reviewer or agent
would plausibly reach for and get wrong.

### Capability group

One of the three independent, self-sufficient units of server capability (`read`, `comment`, `transition`) selected at
startup via `--groups`. Each write-capable group bundles the minimal change-read subset it needs to function on its
own; enabled groups union; all three together form the full capability set. Groups are not ordered — there is no
ladder to climb.
**Avoid:** toolset (github-mcp-server vocabulary; invites importing its `all`/`default` semantics, which we don't
have); tier / level / mode (implies a cumulative ladder — groups are independent and combinable); permission (Gerrit
has its own permission system server-side; groups gate what the MCP exposes, not what Gerrit allows).

### Read group

The capability group exposing change-centric fetch tools. Its boundary is the change: queries, details, files, diffs,
comments. Account, group, and repository-content browsing are outside it by design, not by omission.
**Avoid:** read-only mode (ecosystem term for a global switch like `--read-only`; ours is a group that others combine
with, not a mode the server is in); fetch group (the write groups also fetch — their bundled read subsets).

### Comment group

The capability group exposing comment publication: top-level review messages, inline/file/range comments, threaded
replies, resolving/unresolving threads. Explicitly excludes votes — in Gerrit's API a "review" carries labels, but
this group never sets them.
**Avoid:** review group (Gerrit's SetReview bundles comments AND votes; calling this "review" smuggles voting into the
inert group); reply group (creating new threads is equally in scope).

### Transition group

The capability group exposing operations that transition a change's state or unlock such a transition: votes, submit,
abandon, restore, WIP/ready. Grouped together because every member can move a change toward or away from merging.
**Avoid:** write group (comment is also a write; the distinguishing property is state transition, not mutation);
vote group / state-change group (each names only a subset); destructive group (informal discovery-era name — kept out
of code and docs).

### Trail-leaving

The property of an operation that makes the agent's action visible to other humans on the Gerrit instance — comments,
votes, state changes. The own-changes restriction gates exactly the trail-leaving operations; reads are not
trail-leaving and are never gated by it.
**Avoid:** destructive (a comment leaves a trail but destroys nothing; the criterion is visibility, not damage);
write (correct set today, wrong concept — the restriction exists because humans see the trail, not because state
changed).

### Own-changes restriction

The safety restriction (flag `--own-changes-only`, default `true`) that refuses trail-leaving operations on changes
not owned by the authenticated account. Scopes writes only; reads stay unrestricted.
**Avoid:** author filter (collides with the deferred thread-author reply filter — a different, post-v1 feature that
restricts which comment authors the agent may reply to); ownership check (Gerrit-side ACL flavor; this is an MCP-side
gate).

### Project scoping

The visibility restriction (flag `--projects`) that confines every operation — reads included — to an allowlist of
Gerrit projects. Enforced server-side: a project clause is injected into every change query regardless of what the
agent composed, and direct operations on out-of-scope changes are refused.
**Avoid:** project filter (suggests post-hoc filtering of results; it is enforced query rewriting plus refusal);
visibility (Gerrit's own term for ACL-driven readability; scoping is narrower than what credentials can see).

### Tool filters

The `--include-tools` / `--exclude-tools` lists applied after group resolution. Filters only narrow: exclude removes
tools from the group-resolved set, include keeps only the listed subset of it. A tool outside the enabled groups can
never be activated by a filter.
**Avoid:** allowlist (implies granting capability — include never escalates beyond what groups resolved); enable/
disable flags (suggests per-tool switches independent of groups; filters are subordinate to groups).

### llmxml

The output format of every tool that a model reads: an LLM-digestible subset of XML — line-structured, semantically
tagged text, with no transport or deserialization role and no indentation. Emitted by a purpose-built renderer; there
is no schema and no consumer that parses it back. The one tool that a program reads, `get_review_activity`, returns
JSON whose `content` field is llmxml.
**Avoid:** XML (implies spec compliance and `encoding/xml` serialization — explicitly rejected; no declaration, no
namespaces, no round-tripping); JSON output / structured output for a tool that a model reads (MCP-ecosystem default
assumption; such a tool deliberately emits text for model consumption, not a machine-parseable payload).

### Review activity

What is new on a Gerrit change past a point that the caller keeps: change messages, votes, inline comment threads, and
a status transition. The server reports it through `get_review_activity` when asked and keeps no state; delivering it
into a session is a client's job.
**Avoid:** notification / review notification for the server side (the server notifies nobody — it answers a pull;
only the plugin delivers [review notifications](#review-notifications)).

### Review notifications

The feature that puts new review activity on subscribed changes into the agent's session. A Claude Code plugin polls
the server's `get_review_activity` tool and delivers the result; the server keeps no state and only applies the
`--review-notifications-*` filter flags. It is deliberately not a capability group, because it is client-side
mechanics, not a Gerrit operation class.
**Avoid:** notification group / notification capability (implies a fourth capability group — groups partition Gerrit
operations by trail impact, this partitions nothing); watch feature / watcher (collides with Gerrit's server-side
watched-projects setting, which this is not); channel (the Claude Code channels contract is a different mechanism,
which this feature does not use).

### Subscription

The per-session set of changes the agent asked to be notified about, held by the plugin together with the point per
change that the last report reached. Trail-free — nothing is visible on the Gerrit instance — and bound to the
session. Terminal states (merged, abandoned) end a subscription automatically.
**Avoid:** watch / watch set (Gerrit's "watched projects" is a server-side account setting that emails you — this is
local state the plugin holds for one session); star (Gerrit's starred-changes mechanism, also server-side account
state).
