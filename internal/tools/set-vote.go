package tools

import (
	"context"
	"strings"

	"dev.gaijin.team/go/golib/e"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

var errVoteNoLabel = e.New("vote label must not be empty")

type setVoteInput struct {
	Change  string `json:"change" jsonschema:"Change identifier: numeric ID, project~number, or Change-Id"`
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
				Description: "Set a vote on a Gerrit change: label name plus numeric value, with an " +
					"optional message. Value 0 clears an own vote. Gerrit validates the label and " +
					"range; its refusal is reported verbatim. Refused on changes not owned by the " +
					"authenticated account unless the operator disabled the own-changes restriction.",
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
					return nil, nil, err
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
