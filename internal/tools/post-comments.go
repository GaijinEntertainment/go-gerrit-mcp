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
)

type inlineComment struct {
	File      string `json:"file" jsonschema:"File path; /COMMIT_MSG or /PATCHSET_LEVEL for non-file comments"`
	Message   string `json:"message" jsonschema:"Comment text"`
	Line      int    `json:"line,omitempty" jsonschema:"1-based line, omit for a file-level comment"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"Multi-line comment start, used with end_line"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Multi-line comment end, inclusive"`
	ReplyTo   string `json:"reply_to,omitempty" jsonschema:"Comment id to reply to, from get_change_comments"`
	Resolved  *bool  `json:"resolved,omitempty" jsonschema:"Thread resolution intent; replies inherit when omitted"`
}

type postCommentsInput struct {
	Change   string          `json:"change" jsonschema:"Change identifier: numeric ID, project~number, or Change-Id"`
	Message  string          `json:"message,omitempty" jsonschema:"Top-level review message"`
	Comments []inlineComment `json:"comments,omitempty" jsonschema:"Inline and file comments to publish"`
	Notify   string          `json:"notify,omitempty" jsonschema:"NONE, OWNER, OWNER_REVIEWERS, or ALL; default ALL"`
}

func postComments(c *gerritclient.Client) Tool {
	return Tool{
		Name: NamePostComments,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NamePostComments,
				Description: "Post a review to a Gerrit change in one call: optional top-level message " +
					"plus inline, range, file-level, and reply comments. Replies anchor to comment ids " +
					"from get_change_comments; setting resolved on a reply toggles the thread state. " +
					"Refused on changes not owned by the authenticated account unless the operator " +
					"disabled the own-changes restriction.",
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
		)
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
