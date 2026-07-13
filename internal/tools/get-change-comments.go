package tools

import (
	"context"
	"fmt"
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
	Status string `json:"status,omitempty" jsonschema:"Thread filter: all, resolved, or unresolved; default all"`
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
					"state. The calling account's unpublished draft comments are included, marked " +
					"draft=\"true\" and invisible to anyone else; thread state accounts for them, " +
					"matching what this account's Gerrit UI shows. Comment ids are the reply " +
					"anchors for post_comments; filter status=unresolved to see what still needs " +
					"action.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in getChangeCommentsInput,
			) (*mcp.CallToolResult, any, error) {
				if in.Status == "" {
					in.Status = statusAll
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
}

// flattenComments merges the published and draft per-file comment maps into
// one change-wide pool. Draft entries carry no author in Gerrit's response —
// they are always the caller's — so they are labeled as self.
func flattenComments(published, drafts map[string][]gerrit.CommentInfo, selfLabel string) []changeComment {
	var all []changeComment

	for path, comments := range published {
		for _, ci := range comments {
			all = append(all, changeComment{
				CommentInfo: ci, path: path, authorLabel: accountLabel(ci.Author), draft: false,
			})
		}
	}

	for path, comments := range drafts {
		for _, ci := range comments {
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
}

// buildThreads reconstructs threads the way Gerrit 3.13 does
// (CommentThreads): one change-wide pool keyed by comment id — reply chains
// may cross file paths when a file was renamed between patch sets — where a
// comment whose parent is absent from the pool roots its own thread.
func buildThreads(all []changeComment) []thread {
	byID := make(map[string]changeComment, len(all))
	for _, c := range all {
		byID[c.ID] = c
	}

	var (
		roots    []changeComment
		children = map[string][]changeComment{}
	)

	for _, c := range all {
		if c.InReplyTo != "" {
			if _, ok := byID[c.InReplyTo]; ok {
				children[c.InReplyTo] = append(children[c.InReplyTo], c)
				continue
			}
		}

		roots = append(roots, c)
	}

	slices.SortStableFunc(roots, compareComments)

	threads := make([]thread, 0, len(roots))
	seen := 0

	for _, root := range roots {
		members := expandThread(root, children)

		seen += len(members)

		last := members[len(members)-1]

		threads = append(threads, thread{
			comments:   members,
			unresolved: last.Unresolved != nil && *last.Unresolved,
		})
	}

	// A reference cycle in malformed data leaves comments unreachable from
	// any root; surface them as single-comment threads instead of dropping
	// them silently.
	if seen < len(all) {
		threads = append(threads, orphanThreads(all, threads)...)
	}

	return threads
}

// expandThread walks the reply tree from the root, always emitting the
// earliest unvisited comment next, so parallel reply branches merge
// chronologically while every parent stays ahead of its children.
func expandThread(root changeComment, children map[string][]changeComment) []changeComment {
	var members []changeComment

	frontier := []changeComment{root}

	for len(frontier) > 0 {
		next := 0

		for i := 1; i < len(frontier); i++ {
			if compareComments(frontier[i], frontier[next]) < 0 {
				next = i
			}
		}

		c := frontier[next]

		frontier = append(frontier[:next], frontier[next+1:]...)
		members = append(members, c)
		frontier = append(frontier, children[c.ID]...)
	}

	return members
}

// orphanThreads returns single-comment threads for comments that no built
// thread contains.
func orphanThreads(all []changeComment, threads []thread) []thread {
	reached := map[string]bool{}

	for _, t := range threads {
		for _, c := range t.comments {
			reached[c.ID] = true
		}
	}

	var orphans []thread

	for _, c := range all {
		if !reached[c.ID] {
			orphans = append(orphans, thread{
				comments:   []changeComment{c},
				unresolved: c.Unresolved != nil && *c.Unresolved,
			})
		}
	}

	return orphans
}

// compareComments orders comments chronologically, breaking ties (and absent
// timestamps) by comment id — Gerrit's ordering, deterministic across runs.
func compareComments(a, b changeComment) int {
	switch {
	case a.Updated == nil || b.Updated == nil:
	case a.Updated.Before(b.Updated.Time):
		return -1
	case b.Updated.Before(a.Updated.Time):
		return 1
	default:
	}

	return strings.Compare(a.ID, b.ID)
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

// renderComments groups threads under the file path of their root comment,
// in sorted path order.
func renderComments(in getChangeCommentsInput, threads []thread, drafts int) string {
	byFile := map[string][]thread{}

	for _, t := range threads {
		path := t.comments[0].path

		byFile[path] = append(byFile[path], t)
	}

	paths := make([]string, 0, len(byFile))
	for path := range byFile {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	var (
		rendered    []string
		threadCount int
	)

	for _, path := range paths {
		var fileThreads []string

		for _, t := range byFile[path] {
			if !matchesStatus(t, in.Status) {
				continue
			}

			threadCount++

			fileThreads = append(fileThreads, renderThread(t))
		}

		if len(fileThreads) == 0 {
			continue
		}

		rendered = append(rendered, llmxml.NewElement("file", llmxml.Attr("path", path)).
			WrapText(strings.Join(fileThreads, "\n")).String())
	}

	root := llmxml.NewElement("comments",
		llmxml.Attr("change", in.Change),
		llmxml.Attr("filter", in.Status),
		llmxml.Attr("threads", threadCount),
		llmxml.Attr("drafts", drafts),
	)

	if len(rendered) == 0 {
		return root.String()
	}

	return root.WrapText(strings.Join(rendered, "\n")).String()
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
