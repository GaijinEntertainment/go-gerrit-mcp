package tools

import (
	"context"
	"strings"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

var (
	errUnknownAction = e.New("unknown action, expected submit, abandon, restore, wip, or ready")
	errSubmitMessage = e.New("submit does not accept a message; vote or comment separately")
)

type transitionChangeInput struct {
	Change  string `json:"change" jsonschema:"Change identifier: numeric ID, project~number, or Change-Id"`
	Action  string `json:"action" jsonschema:"One of: submit, abandon, restore, wip, ready"`
	Message string `json:"message,omitempty" jsonschema:"Optional message; not accepted for submit"`
}

func transitionChange(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameTransitionChange,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameTransitionChange,
				Description: "Transition a Gerrit change's state. Actions and the states they move " +
					"between: submit (NEW -> MERGED, requires satisfied submit requirements), " +
					"abandon (NEW -> ABANDONED), restore (ABANDONED -> NEW), wip (marks NEW change " +
					"work-in-progress), ready (work-in-progress -> ready for review). An optional " +
					"message accompanies every action except submit. Gerrit's refusal (blocked " +
					"submit, restore of a merged change) is reported verbatim. Refused on changes " +
					"not owned by the authenticated account unless the operator disabled the " +
					"own-changes restriction.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in transitionChangeInput,
			) (*mcp.CallToolResult, any, error) {
				action := strings.ToLower(strings.TrimSpace(in.Action))

				status, err := runTransition(ctx, c, action, in)
				if err != nil {
					return nil, nil, err
				}

				attrs := []llmxml.Attribute{
					llmxml.Attr("change", in.Change),
					llmxml.Attr("action", action),
				}
				if status != "" {
					attrs = append(attrs, llmxml.Attr("status", status))
				}

				return textResult(llmxml.NewElement("change_transitioned", attrs...).String()), nil, nil
			})
		},
	}
}

// runTransition maps the action enum onto the client operation and reports
// the change status when the endpoint returns one.
func runTransition(ctx context.Context, c *gerritclient.Client, action string, in transitionChangeInput,
) (string, error) {
	switch action {
	case "submit":
		if strings.TrimSpace(in.Message) != "" {
			return "", errSubmitMessage
		}

		info, err := c.SubmitChange(ctx, in.Change)
		if err != nil {
			return "", err
		}

		return info.Status, nil

	case "abandon":
		info, err := c.AbandonChange(ctx, in.Change, in.Message)
		if err != nil {
			return "", err
		}

		return info.Status, nil

	case "restore":
		info, err := c.RestoreChange(ctx, in.Change, in.Message)
		if err != nil {
			return "", err
		}

		return info.Status, nil

	case "wip":
		return "", c.SetWorkInProgress(ctx, in.Change, in.Message)
	case "ready":
		return "", c.SetReadyForReview(ctx, in.Change, in.Message)
	default:
		return "", errUnknownAction.WithField("action", in.Action)
	}
}
