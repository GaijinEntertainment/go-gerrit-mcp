package tools

import (
	"context"
	"slices"
	"strings"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

var (
	errEmptyReview    = e.New("nothing to post: provide a message, comments, or both")
	errInvalidNotify  = e.New("invalid notify value, expected NONE, OWNER, OWNER_REVIEWERS, or ALL")
	errLineAndRange   = e.New("comment cannot carry both line and start_line/end_line")
	errInvalidRange   = e.New("end_line must be greater than or equal to start_line")
	errUnknownReplyTo = e.New("reply targets do not exist on the change")
	errCommentNoText  = e.New("comment message must not be empty")
	errCommentNoFile  = e.New("comment file must not be empty")
	// errCommentFileUnknown guards against Gerrit's silent acceptance of
	// comments on paths outside the change, which no reviewer ever sees.
	errCommentFileUnknown = e.New("comment file is not part of the current revision")
)

type inlineComment struct {
	File      string `json:"file" jsonschema:"File path as list_change_files reports it; /COMMIT_MSG or /PATCHSET_LEVEL for non-file comments"`
	Message   string `json:"message" jsonschema:"Comment text"`
	Line      int    `json:"line,omitempty" jsonschema:"1-based line in the new version of the file; omit for a file-level comment"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"First line of a multi-line comment (new version of the file), used with end_line"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Last line of a multi-line comment, inclusive"`
	ReplyTo   string `json:"reply_to,omitempty" jsonschema:"Comment id to reply to, from get_change_comments"`
	Resolved  *bool  `json:"resolved,omitempty" jsonschema:"true resolves the thread, false reopens it; replies inherit the thread state when omitted"`
}

type postCommentsInput struct {
	Change   string          `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
	Message  string          `json:"message,omitempty" jsonschema:"Top-level review message, shown on the change rather than on a file"`
	Comments []inlineComment `json:"comments,omitempty" jsonschema:"Inline, range, file-level, and reply comments to publish"`
	Notify   string          `json:"notify,omitempty" jsonschema:"Who is notified by email: NONE, OWNER, OWNER_REVIEWERS, or ALL; default ALL"`
}

func postComments(c *gerritclient.Client) Tool {
	return Tool{
		Name: NamePostComments,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NamePostComments,
				Description: "Post a review to a Gerrit change in one call, published immediately and " +
					"visible to everyone on the change: optional top-level message plus inline " +
					"(file and line), range, file-level, and reply comments. New comments must " +
					"name a file exactly as list_change_files reports it (or /COMMIT_MSG, " +
					"/PATCHSET_LEVEL); replies anchor to comment ids from get_change_comments, and " +
					"setting resolved on a reply toggles the thread state. Refused on changes not " +
					"owned by the authenticated account unless the operator disabled the " +
					"own-changes restriction.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in postCommentsInput,
			) (*mcp.CallToolResult, any, error) {
				input, err := buildReviewInput(ctx, c, in)
				if err != nil {
					return nil, nil, err
				}

				if _, err := c.SetReview(ctx, in.Change, "", input); err != nil {
					return nil, nil, err
				}

				return textResult(renderReviewAck(in)), nil, nil
			})
		},
	}
}

func buildReviewInput(ctx context.Context, c *gerritclient.Client, in postCommentsInput) (*gerrit.ReviewInput, error) {
	if strings.TrimSpace(in.Message) == "" && len(in.Comments) == 0 {
		return nil, errEmptyReview
	}

	notify := strings.ToUpper(strings.TrimSpace(in.Notify))
	if notify != "" && !slices.Contains([]string{"NONE", "OWNER", "OWNER_REVIEWERS", "ALL"}, notify) {
		return nil, errInvalidNotify.WithField("notify", in.Notify)
	}

	comments, err := buildComments(in.Comments)
	if err != nil {
		return nil, err
	}

	if err := validateCommentFiles(ctx, c, in); err != nil {
		return nil, err
	}

	if err := validateReplyTargets(ctx, c, in); err != nil {
		return nil, err
	}

	return &gerrit.ReviewInput{
		Message:  in.Message,
		Comments: comments,
		Notify:   notify,
	}, nil
}

func buildComments(comments []inlineComment) (map[string][]gerrit.CommentInput, error) {
	if len(comments) == 0 {
		return nil, nil //nolint:nilnil // absent map means no inline comments in the review
	}

	byFile := make(map[string][]gerrit.CommentInput, len(comments))

	for _, comment := range comments {
		ci, err := buildComment(comment)
		if err != nil {
			return nil, err
		}

		byFile[comment.File] = append(byFile[comment.File], ci)
	}

	return byFile, nil
}

func buildComment(comment inlineComment) (gerrit.CommentInput, error) {
	if strings.TrimSpace(comment.File) == "" {
		return gerrit.CommentInput{}, errCommentNoFile
	}

	if strings.TrimSpace(comment.Message) == "" {
		return gerrit.CommentInput{}, errCommentNoText.WithField("file", comment.File)
	}

	hasRange := comment.StartLine > 0 || comment.EndLine > 0

	if comment.Line > 0 && hasRange {
		return gerrit.CommentInput{}, errLineAndRange.WithField("file", comment.File)
	}

	if hasRange && comment.EndLine < comment.StartLine {
		return gerrit.CommentInput{}, errInvalidRange.WithFields(
			fields.F("file", comment.File),
			fields.F("start_line", comment.StartLine),
			fields.F("end_line", comment.EndLine),
		)
	}

	ci := gerrit.CommentInput{
		Message:   comment.Message,
		Line:      comment.Line,
		InReplyTo: comment.ReplyTo,
	}

	if hasRange {
		ci.Range = &gerrit.CommentRange{
			StartLine:      comment.StartLine,
			StartCharacter: 0,
			EndLine:        comment.EndLine,
			EndCharacter:   0,
		}
	}

	if comment.Resolved != nil {
		unresolved := !*comment.Resolved

		ci.Unresolved = &unresolved
	}

	return ci, nil
}

// validateReplyTargets refuses replies anchored to comment ids that do not
// exist on the change, so typos surface as errors instead of orphan threads.
func validateReplyTargets(ctx context.Context, c *gerritclient.Client, in postCommentsInput) error {
	var targets []string

	for _, comment := range in.Comments {
		if comment.ReplyTo != "" {
			targets = append(targets, comment.ReplyTo)
		}
	}

	if len(targets) == 0 {
		return nil
	}

	existing, err := c.ListChangeComments(ctx, in.Change)
	if err != nil {
		return err
	}

	known := map[string]bool{}

	for _, comments := range existing {
		for _, comment := range comments {
			known[comment.ID] = true
		}
	}

	var missing []string

	for _, target := range targets {
		if !known[target] {
			missing = append(missing, target)
		}
	}

	if len(missing) > 0 {
		return errUnknownReplyTo.WithFields(
			fields.F("change", in.Change),
			fields.F("ids", strings.Join(missing, ",")),
			fields.F("hint", "comment ids may be stale; get_change_comments returns current anchors"),
		)
	}

	return nil
}

// magicPath reports whether the path is one of Gerrit's virtual files,
// commentable on every change.
func magicPath(path string) bool {
	switch path {
	case pathCommitMsg, pathPatchsetLevel, "/MERGE_LIST":
		return true
	default:
		return false
	}
}

// validateCommentFiles refuses new comments on files absent from the current
// revision — Gerrit silently accepts them and the comment is never seen next
// to code. Replies are exempt: their thread may anchor to a file of an older
// patch set. Costs one file-list fetch, only when new file comments exist.
func validateCommentFiles(ctx context.Context, c *gerritclient.Client, in postCommentsInput) error {
	var unchecked []string

	for _, comment := range in.Comments {
		if comment.ReplyTo == "" && !magicPath(comment.File) {
			unchecked = append(unchecked, comment.File)
		}
	}

	if len(unchecked) == 0 {
		return nil
	}

	files, err := c.ListFiles(ctx, in.Change, "")
	if err != nil {
		return err
	}

	for _, file := range unchecked {
		if _, ok := files[file]; ok {
			continue
		}

		res := errCommentFileUnknown.WithFields(
			fields.F("change", in.Change),
			fields.F("file", file),
		)

		paths := make([]string, 0, len(files))
		for path := range files {
			paths = append(paths, path)
		}

		if near := proposals(file, paths); len(near) > 0 {
			return res.WithField("did_you_mean", strings.Join(near, ", "))
		}

		return res.WithField("hint", "list_change_files names the files of the current revision")
	}

	return nil
}

func renderReviewAck(in postCommentsInput) string {
	notify := strings.ToUpper(strings.TrimSpace(in.Notify))
	if notify == "" {
		notify = "ALL"
	}

	return llmxml.NewElement("review_posted",
		llmxml.Attr("change", in.Change),
		llmxml.Attr("message", strings.TrimSpace(in.Message) != ""),
		llmxml.Attr("comments", len(in.Comments)),
		llmxml.Attr("notify", notify),
	).String()
}
