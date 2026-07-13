package tools

import (
	"context"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

type unsubscribeChangeInput struct {
	Change string `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
}

// UnsubscribeChange returns the tool ending a review-notifications
// subscription. Unsubscribing a change that was not subscribed is not an
// error — the end state is the same either way, and the acknowledgement says
// which case it was.
func UnsubscribeChange(c *gerritclient.Client, store *notifications.Store) Tool {
	return Tool{
		Name: NameUnsubscribeChange,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameUnsubscribeChange,
				Description: "End this session's review-notifications subscription to a Gerrit " +
					"change: use it when the change no longer needs following — its activity " +
					"stops arriving immediately. Unsubscribing a change that was not subscribed " +
					"changes nothing and says so. Merged and abandoned changes end their own " +
					"subscriptions; those need no unsubscribe.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in unsubscribeChangeInput,
			) (*mcp.CallToolResult, any, error) {
				number, err := resolveChangeNumber(ctx, c, in.Change)
				if err != nil {
					return nil, nil, err
				}

				removed := store.Remove(number)

				el := llmxml.NewElement("unsubscribed", llmxml.Attr("change", number))

				if removed {
					el.InlineText("No further review notifications for this change will arrive in this session.")
				} else {
					el.Attr(llmxml.Attr("was_subscribed", false))
					el.InlineText("This change was not subscribed; nothing changed.")
				}

				return textResult(el.String()), nil, nil
			})
		},
	}
}

// resolveChangeNumber turns any change identifier into the change number the
// subscription store is keyed by. A numeric identifier resolves locally, so
// unsubscribing never depends on the change still being fetchable; other
// identifier forms cost one fetch.
func resolveChangeNumber(ctx context.Context, c *gerritclient.Client, id string) (int, error) {
	if number, err := strconv.Atoi(strings.TrimSpace(id)); err == nil {
		return number, nil
	}

	info, err := c.GetChange(ctx, id)
	if err != nil {
		return 0, err
	}

	return info.Number, nil
}
