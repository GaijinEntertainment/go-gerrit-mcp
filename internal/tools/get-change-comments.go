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
					"state. Comment ids are the reply anchors for post_comments; filter " +
					"status=unresolved to see what still needs action.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in getChangeCommentsInput,
			) (*mcp.CallToolResult, any, error) {
				if in.Status == "" {
					in.Status = statusAll
				}

				if in.Status != statusAll && in.Status != statusResolved && in.Status != statusUnresolved {
					return nil, nil, errInvalidStatus.WithField("status", in.Status)
				}

				comments, err := c.ListChangeComments(ctx, in.Change)
				if err != nil {
					return nil, nil, err
				}

				return textResult(renderComments(in, comments)), nil, nil
			})
		},
	}
}

// thread is one comment thread: the root comment and its replies in
// chronological order. Its resolved state is the state of the last comment.
type thread struct {
	comments   []gerrit.CommentInfo
	unresolved bool
}

// buildThreads reconstructs threads from a flat comment list. Comments are
// ordered chronologically; each reply chain is walked to its root, and a
// reply whose parent is missing starts its own thread.
func buildThreads(comments []gerrit.CommentInfo) []thread {
	ordered := make([]gerrit.CommentInfo, len(comments))
	copy(ordered, comments)

	slices.SortStableFunc(ordered, compareUpdated)

	parent := make(map[string]string, len(ordered))
	for _, comment := range ordered {
		parent[comment.ID] = comment.InReplyTo
	}

	var (
		roots   []string
		grouped = map[string][]gerrit.CommentInfo{}
	)

	for _, comment := range ordered {
		root := rootOf(parent, comment.ID)
		if _, ok := grouped[root]; !ok {
			roots = append(roots, root)
		}

		grouped[root] = append(grouped[root], comment)
	}

	threads := make([]thread, 0, len(roots))

	for _, root := range roots {
		group := grouped[root]
		last := group[len(group)-1]

		threads = append(threads, thread{
			comments:   group,
			unresolved: last.Unresolved != nil && *last.Unresolved,
		})
	}

	return threads
}

func compareUpdated(a, b gerrit.CommentInfo) int {
	switch {
	case a.Updated == nil || b.Updated == nil:
		return 0
	case a.Updated.Before(b.Updated.Time):
		return -1
	case b.Updated.Before(a.Updated.Time):
		return 1
	default:
		return 0
	}
}

// rootOf walks a reply chain to its root; the iteration cap guards against
// reference cycles in malformed data.
func rootOf(parent map[string]string, id string) string {
	for range len(parent) {
		up, ok := parent[id]
		if !ok || up == "" {
			return id
		}

		id = up
	}

	return id
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

func renderComments(in getChangeCommentsInput, byFile map[string][]gerrit.CommentInfo) string {
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
		threads := buildThreads(byFile[path])

		var fileThreads []string

		for _, t := range threads {
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
			llmxml.Attr("author", accountLabel(comment.Author)),
		)

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
