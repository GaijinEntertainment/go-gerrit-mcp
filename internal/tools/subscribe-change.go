package tools

import (
	"context"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

// ErrAlreadySubscribed reports a subscribe attempt on a change this session
// already follows.
var ErrAlreadySubscribed = e.New("change is already subscribed in this session")

// ErrTerminalChange reports a subscribe attempt on a merged or abandoned
// change, whose subscription would never fire.
var ErrTerminalChange = e.New("change is in a terminal state; nothing left to notify about")

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
				Description: "Subscribe this session to a Gerrit change. Call it right after pushing " +
					"a change for review, or when a review outcome you depend on is pending — an " +
					"approval, a CI verdict, a reviewer's reply. New activity then arrives in the " +
					"session by itself: change messages, votes, inline comment threads, and status " +
					"transitions, carried whole — never poll the read tools for a subscribed " +
					"change. When the change is merged or abandoned, a final notification announces " +
					"it and the subscription ends automatically; subscribing to a change already in " +
					"such a state is refused. The subscription is per-session and in-memory: it " +
					"leaves no trace on Gerrit, ends with the session, and after a server restart " +
					"you must subscribe again.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in subscribeChangeInput,
			) (*mcp.CallToolResult, any, error) {
				info, err := c.GetChange(ctx, in.Change)
				if err != nil {
					return nil, nil, err
				}

				if notifications.IsTerminal(info.Status) {
					return nil, nil, ErrTerminalChange.WithFields(
						fields.F("change", info.Number),
						fields.F("status", info.Status),
					)
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
