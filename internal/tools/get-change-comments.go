package tools

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dev.gaijin.team/go/golib/e"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

// Thread status filter values.
const (
	statusAll        = "all"
	statusResolved   = "resolved"
	statusUnresolved = "unresolved"
)

var errInvalidStatus = e.New("invalid status filter, expected all, resolved, or unresolved")

type getChangeCommentsInput struct {
	Change string `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
	Status string `json:"status,omitempty" jsonschema:"Thread filter: all, resolved, or unresolved; default unresolved"`
}

func getChangeComments(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameGetChangeComments,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameGetChangeComments,
				Description: "List a Gerrit change's inline review comments — the code discussion " +
					"anchored to files and lines, distinct from the change messages get_change " +
					"shows — grouped by file and reconstructed into threads with their resolved " +
					"state. By default only unresolved threads return: the discussion still " +
					"needing action. status=all adds the settled history — on long reviews that " +
					"can be very large, so reach for it only when the resolved context matters; " +
					"status=resolved lists the settled threads alone. The calling account's " +
					"unpublished draft comments are included, marked draft=\"true\" and invisible " +
					"to anyone else; thread state accounts for them, matching what this account's " +
					"Gerrit UI shows. Unresolved threads render before resolved ones; threads " +
					"follow their latest activity and comments their history. Comment ids are the " +
					"reply anchors for post_comments.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in getChangeCommentsInput,
			) (*mcp.CallToolResult, any, error) {
				// Unresolved is the default: it is the actionable subset, and
				// the full history of a long review can dwarf the context it
				// lands in.
				if in.Status == "" {
					in.Status = statusUnresolved
				}

				if in.Status != statusAll && in.Status != statusResolved && in.Status != statusUnresolved {
					return nil, nil, errInvalidStatus.WithField("status", in.Status)
				}

				published, err := c.ListChangeComments(ctx, in.Change)
				if err != nil {
					return nil, nil, err
				}

				drafts, err := c.ListChangeDrafts(ctx, in.Change)
				if err != nil {
					return nil, nil, err
				}

				all := flattenComments(published, drafts, accountLabel(c.Self()))

				return textResult(renderComments(in, buildThreads(all), draftCount(all))), nil, nil
			})
		},
	}
}

// changeComment is one comment of a change annotated with the file path it
// anchors to and whether it is the caller's unpublished draft.
type changeComment struct {
	gerrit.CommentInfo

	path        string
	authorLabel string
	draft       bool
	// sortRange is the range the change screen sorts by: the comment's own,
	// or the parent's when sanitiseRanges inherits it. Rendering keeps the
	// comment's real Range.
	sortRange *gerrit.CommentRange `exhaustruct:"optional"`
}

// flattenComments merges the published and draft per-file comment maps into
// one change-wide pool, preserving the order the change screen sees: files
// in sorted path order (Gerrit emits sorted keys), arrays as delivered,
// drafts after published comments. Order matters — sanitiseRanges is
// deliberately order-dependent. Draft entries carry no author in Gerrit's
// response — they are always the caller's — so they are labeled as self.
func flattenComments(published, drafts map[string][]gerrit.CommentInfo, selfLabel string) []changeComment {
	var all []changeComment

	for _, path := range slices.Sorted(maps.Keys(published)) {
		for _, ci := range published[path] {
			all = append(all, changeComment{
				CommentInfo: ci, path: path, authorLabel: accountLabel(ci.Author), draft: false,
			})
		}
	}

	for _, path := range slices.Sorted(maps.Keys(drafts)) {
		for _, ci := range drafts[path] {
			all = append(all, changeComment{
				CommentInfo: ci, path: path, authorLabel: selfLabel, draft: true,
			})
		}
	}

	return all
}

func draftCount(all []changeComment) int {
	count := 0

	for _, c := range all {
		if c.draft {
			count++
		}
	}

	return count
}

// thread is one comment thread in Gerrit's order: root first, reply branches
// merged chronologically with comment-id tiebreak. Its resolved state is the
// state of the thread's last comment — drafts included, which is exactly the
// state the caller's change screen shows.
type thread struct {
	comments   []changeComment
	unresolved bool
	// root is the comment that anchors the thread in the UI's grouping; its
	// path and timestamp place the thread in the output.
	root changeComment
}

// Gerrit's virtual file paths; the change screen pins them before real files.
const (
	pathPatchsetLevel = "/PATCHSET_LEVEL"
	pathCommitMsg     = "/COMMIT_MSG"
)

// buildThreads groups comments the way the change screen does — a
// bug-compatible replica of polygerrit's createCommentThreads (stable-3.13
// comment-util.ts), because the product contract is "any thread the UI shows
// as unresolved is unresolved" (issue #44). Comments are sorted (after the
// order-dependent range patching of sanitiseRanges) and attached to the
// thread of their parent only if that parent sorted earlier; otherwise the
// comment roots a new thread — including replies whose parent merely sorts
// later, which is exactly how the UI shatters such threads. Gerrit's
// server-side CommentThreads would merge them; the screen does not, and the
// screen is the contract.
func buildThreads(all []changeComment) []thread {
	sanitiseRanges(all)

	sorted := make([]changeComment, len(all))
	copy(sorted, all)
	slices.SortStableFunc(sorted, compareUI)

	var (
		acc       [][]changeComment
		threadIdx = map[string]int{}
	)

	for _, c := range sorted {
		if c.InReplyTo != "" {
			if ti, ok := threadIdx[c.InReplyTo]; ok {
				acc[ti] = append(acc[ti], c)
				threadIdx[c.ID] = ti

				continue
			}
		}

		threadIdx[c.ID] = len(acc)
		acc = append(acc, []changeComment{c})
	}

	threads := make([]thread, 0, len(acc))

	for _, members := range acc {
		// State and grouping follow the UI's ordering; only after both are
		// fixed do the comments re-sort chronologically for display.
		last := members[len(members)-1]
		root := members[0]

		slices.SortStableFunc(members, compareChrono)

		threads = append(threads, thread{
			comments:   members,
			unresolved: last.Unresolved != nil && *last.Unresolved,
			root:       root,
		})
	}

	return threads
}

// compareChrono orders comments by time, falling back to comment id only on
// equal timestamps — the display order of comments within a thread and of
// threads within a file.
func compareChrono(a, b changeComment) int {
	if c := compareUpdated(a.Updated, b.Updated); c != 0 {
		return c
	}

	return strings.Compare(a.ID, b.ID)
}

// sanitiseRanges mirrors polygerrit's pre-sort pass: a rangeless reply
// inherits its parent's range so same-location comments sort together. The
// pass is deliberately order-dependent — a reply processed before its parent
// inherits nothing — because the change screen's threading depends on
// exactly that behavior.
func sanitiseRanges(all []changeComment) {
	byID := make(map[string]int, len(all))

	for i := range all {
		byID[all[i].ID] = i
		all[i].sortRange = all[i].Range
	}

	for i := range all {
		if all[i].InReplyTo == "" || all[i].sortRange != nil {
			continue
		}

		if j, ok := byID[all[i].InReplyTo]; ok && all[j].sortRange != nil {
			all[i].sortRange = all[j].sortRange
		}
	}
}

// compareUI replicates polygerrit's comment ordering: patchset-level path
// first, then path, patch set, line (a range counts by its end line), range
// coordinates — absent numbers sort before present ones throughout — then
// drafts after published, timestamp, and comment id.
func compareUI(a, b changeComment) int {
	if c := comparePaths(a.path, b.path); c != 0 {
		return c
	}

	if c := compareOptInt(a.PatchSet, a.PatchSet > 0, b.PatchSet, b.PatchSet > 0); c != 0 {
		return c
	}

	l1, h1 := lineOf(a)
	l2, h2 := lineOf(b)

	if c := compareOptInt(l1, h1, l2, h2); c != 0 {
		return c
	}

	if c := compareRanges(a.sortRange, b.sortRange); c != 0 {
		return c
	}

	if a.draft != b.draft {
		if a.draft {
			return 1
		}

		return -1
	}

	if c := compareUpdated(a.Updated, b.Updated); c != 0 {
		return c
	}

	return strings.Compare(a.ID, b.ID)
}

// comparePaths orders file paths with the patchset-level pseudo-file pinned
// first, as the change screen does.
func comparePaths(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == pathPatchsetLevel:
		return -1
	case b == pathPatchsetLevel:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// compareUpdated orders timestamps, treating an absent one as equal.
func compareUpdated(a, b *gerrit.Timestamp) int {
	switch {
	case a == nil || b == nil:
		return 0
	case a.Before(b.Time):
		return -1
	case b.Before(a.Time):
		return 1
	default:
		return 0
	}
}

// compareOptInt orders two optional numbers with absent values first —
// polygerrit's compareNumber, where absent is JavaScript's undefined.
func compareOptInt(a int, hasA bool, b int, hasB bool) int {
	switch {
	case !hasA && !hasB:
		return 0
	case !hasA:
		return -1
	case !hasB:
		return 1
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// lineOf resolves the line a comment anchors to: its own line, or the end
// line of its range, or nothing for file-level comments.
func lineOf(c changeComment) (int, bool) {
	if c.Line > 0 {
		return c.Line, true
	}

	if c.sortRange != nil {
		return c.sortRange.EndLine, true
	}

	return 0, false
}

// compareRanges orders range coordinates the way polygerrit does:
// start line, end character, start character — a missing range sorts before
// any present one at each step.
func compareRanges(a, b *gerrit.CommentRange) int {
	coords := []func(*gerrit.CommentRange) int{
		func(r *gerrit.CommentRange) int { return r.StartLine },
		func(r *gerrit.CommentRange) int { return r.EndCharacter },
		func(r *gerrit.CommentRange) int { return r.StartCharacter },
	}

	for _, coord := range coords {
		var n1, n2 int

		if a != nil {
			n1 = coord(a)
		}

		if b != nil {
			n2 = coord(b)
		}

		if c := compareOptInt(n1, a != nil, n2, b != nil); c != 0 {
			return c
		}
	}

	return 0
}

func matchesStatus(t thread, status string) bool {
	switch status {
	case statusResolved:
		return !t.unresolved
	case statusUnresolved:
		return t.unresolved
	default:
		return true
	}
}

// renderComments emits attention first: an unresolved section, then a
// resolved one — a file with both kinds of threads appears in each. Inside a
// section files order as the change screen names them (patchset level, then
// the commit message, then paths); threads within a file follow the time of
// their latest comment; comments within a thread follow history. Comment ids
// break ties everywhere.
func renderComments(in getChangeCommentsInput, threads []thread, drafts int) string {
	var unresolved, resolved []thread

	for _, t := range threads {
		if !matchesStatus(t, in.Status) {
			continue
		}

		if t.unresolved {
			unresolved = append(unresolved, t)
		} else {
			resolved = append(resolved, t)
		}
	}

	var sections []string

	if s := renderSection("unresolved", unresolved); s != "" {
		sections = append(sections, s)
	}

	if s := renderSection("resolved", resolved); s != "" {
		sections = append(sections, s)
	}

	root := llmxml.NewElement("comments",
		llmxml.Attr("change", in.Change),
		llmxml.Attr("filter", in.Status),
		llmxml.Attr("threads", len(unresolved)+len(resolved)),
		llmxml.Attr("drafts", drafts),
	)

	if len(sections) == 0 {
		return root.String()
	}

	return root.WrapText(strings.Join(sections, "\n")).String()
}

// renderSection wraps one resolution state's threads, grouped by the file
// path of each thread's root comment.
func renderSection(name string, threads []thread) string {
	if len(threads) == 0 {
		return ""
	}

	byFile := map[string][]thread{}
	for _, t := range threads {
		byFile[t.root.path] = append(byFile[t.root.path], t)
	}

	paths := make([]string, 0, len(byFile))
	for path := range byFile {
		paths = append(paths, path)
	}

	slices.SortStableFunc(paths, compareRenderPaths)

	rendered := make([]string, 0, len(paths))

	for _, path := range paths {
		fileThreads := byFile[path]
		slices.SortStableFunc(fileThreads, compareThreads)

		lines := make([]string, 0, len(fileThreads))
		for _, t := range fileThreads {
			lines = append(lines, renderThread(t))
		}

		rendered = append(rendered, llmxml.NewElement("file", llmxml.Attr("path", path)).
			WrapText(strings.Join(lines, "\n")).String())
	}

	return llmxml.NewElement(name, llmxml.Attr("count", len(threads))).
		WrapText(strings.Join(rendered, "\n")).String()
}

// Display ranks for file paths: the change level first, the commit message
// second, everything else by path.
const (
	rankPatchsetLevel = iota
	rankCommitMsg
	rankFilePath
)

// compareRenderPaths orders files for display by rank, then by path.
func compareRenderPaths(a, b string) int {
	rank := func(p string) int {
		switch p {
		case pathPatchsetLevel:
			return rankPatchsetLevel
		case pathCommitMsg:
			return rankCommitMsg
		default:
			return rankFilePath
		}
	}

	if ra, rb := rank(a), rank(b); ra != rb {
		return ra - rb
	}

	return strings.Compare(a, b)
}

// compareThreads orders threads within a file by the time of their latest
// comment — the conversation in the order it last moved.
func compareThreads(a, b thread) int {
	return compareChrono(a.comments[len(a.comments)-1], b.comments[len(b.comments)-1])
}

func renderThread(t thread) string {
	rendered := make([]string, 0, len(t.comments))

	for _, comment := range t.comments {
		el := llmxml.NewElement("comment",
			llmxml.Attr("id", comment.ID),
			llmxml.Attr("author", comment.authorLabel),
		)

		if comment.draft {
			el.Attr(llmxml.Attr("draft", true))
		}

		if comment.Updated != nil {
			el.Attr(llmxml.Attr("date", timestamp(*comment.Updated)))
		}

		if comment.PatchSet > 0 {
			el.Attr(llmxml.Attr("patch_set", comment.PatchSet))
		}

		if comment.Range != nil {
			el.Attr(llmxml.Attr("lines", rangeLabel(comment.Range)))
		} else if comment.Line > 0 {
			el.Attr(llmxml.Attr("line", comment.Line))
		}

		if comment.InReplyTo != "" {
			el.Attr(llmxml.Attr("in_reply_to", comment.InReplyTo))
		}

		rendered = append(rendered, el.WrapText(comment.Message).String())
	}

	return llmxml.NewElement("thread", llmxml.Attr("resolved", !t.unresolved)).
		WrapText(strings.Join(rendered, "\n")).String()
}

func rangeLabel(r *gerrit.CommentRange) string {
	return fmt.Sprintf("%d-%d", r.StartLine, r.EndLine)
}
