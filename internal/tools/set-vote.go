package tools

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"dev.gaijin.team/go/golib/e"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

var errVoteNoLabel = e.New("vote label must not be empty")

type setVoteInput struct {
	Change  string `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
	Label   string `json:"label" jsonschema:"Label name, e.g. Code-Review or Verified"`
	Value   int    `json:"value" jsonschema:"Numeric vote within the label's range; 0 clears an own vote"`
	Message string `json:"message,omitempty" jsonschema:"Optional message accompanying the vote"`
}

func setVote(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameSetVote,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameSetVote,
				Description: "Set this account's vote on one label of a Gerrit change, e.g. " +
					"Code-Review 2 or Verified 1; each label's range is configured per project, " +
					"commonly -2..+2 for Code-Review. Value 0 clears the account's own vote. An " +
					"unknown label or out-of-range value is refused by Gerrit verbatim and the " +
					"error lists the change's configured labels. Refused on changes not owned by " +
					"the authenticated account unless the operator disabled the own-changes " +
					"restriction.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in setVoteInput,
			) (*mcp.CallToolResult, any, error) {
				label := strings.TrimSpace(in.Label)
				if label == "" {
					return nil, nil, errVoteNoLabel
				}

				input := &gerrit.ReviewInput{
					Message: in.Message,
					Labels:  map[string]int{label: in.Value},
				}

				if _, err := c.SetReview(ctx, in.Change, "", input); err != nil {
					return nil, nil, enrichVoteError(ctx, c, in.Change, err)
				}

				return textResult(llmxml.NewElement("vote_set",
					llmxml.Attr("change", in.Change),
					llmxml.Attr("label", label),
					llmxml.Attr("value", in.Value),
				).String()), nil, nil
			})
		},
	}
}

// enrichVoteError appends the change's configured labels to a Gerrit vote
// rejection, so a wrong label or range needs no second guess. Best-effort:
// an unfetchable label set returns the error unchanged.
func enrichVoteError(ctx context.Context, c *gerritclient.Client, change string, err error) error {
	if gerritclient.APIStatus(err) != http.StatusBadRequest {
		return err
	}

	info, ierr := c.GetChange(ctx, change)
	if ierr != nil || len(info.Labels) == 0 {
		return err
	}

	names := make([]string, 0, len(info.Labels))
	for name := range info.Labels {
		names = append(names, name)
	}

	slices.Sort(names)

	return e.From(err).WithField("configured_labels", strings.Join(names, ", "))
}
