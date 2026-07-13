package tools

import (
	"context"

	"dev.gaijin.team/go/golib/e"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

// ErrAlreadySubscribed reports a subscribe attempt on a change this session
// already follows.
var ErrAlreadySubscribed = e.New("change is already subscribed in this session")

type subscribeChangeInput struct {
	Change string `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
}

// SubscribeChange returns the review-notifications subscription tool. It is
// gated by the review-notifications flag family rather than a capability
// group, so it is not part of All (docs/glossary.md: Review notifications).
func SubscribeChange(c *gerritclient.Client, store *notifications.Store) Tool {
	return Tool{
		Name: NameSubscribeChange,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameSubscribeChange,
				Description: "Subscribe this session to a Gerrit change: from now on its review " +
					"activity is pushed into the session as it happens — no polling needed. The " +
					"subscription is per-session and in-memory; it ends with the session, and " +
					"after a server restart you must subscribe again.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in subscribeChangeInput,
			) (*mcp.CallToolResult, any, error) {
				info, err := c.GetChange(ctx, in.Change)
				if err != nil {
					return nil, nil, err
				}

				if !store.Add(info.Number, notifications.NewCursor(info.Updated.Time, info.Status)) {
					return nil, nil, ErrAlreadySubscribed.WithField("change", info.Number)
				}

				return textResult(renderSubscribed(info)), nil, nil
			})
		},
	}
}

// renderSubscribed acknowledges a new subscription with the review state the
// cursor starts from.
func renderSubscribed(ci *gerrit.ChangeInfo) string {
	el := llmxml.NewElement("subscribed",
		llmxml.Attr("change", ci.Number),
		llmxml.Attr("project", ci.Project),
		llmxml.Attr("status", ci.Status),
	)

	if rev, ok := ci.Revisions[ci.CurrentRevision]; ok {
		el.Attr(llmxml.Attr("patch_set", rev.Number))
	}

	el.InlineText("Review activity on this change now arrives in this session automatically.")

	return el.String()
}
