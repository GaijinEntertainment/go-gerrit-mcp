package tools

import (
	"context"
	"slices"
	"strings"
	"time"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

type getChangeInput struct {
	Change string `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
}

func getChange(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameGetChange,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameGetChange,
				Description: "Fetch one Gerrit change's review state: status, owner, submittability, " +
					"labels with votes, reviewers, and the change-message timeline. Change messages " +
					"are patch-set and reviewer announcements, not the inline code discussion — " +
					"read that with get_change_comments. Also reports the current revision SHA, " +
					"usable as the revision argument of other tools.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in getChangeInput) (*mcp.CallToolResult, any, error) {
				info, err := c.GetChange(ctx, in.Change)
				if err != nil {
					return nil, nil, err
				}

				return textResult(renderChange(info)), nil, nil
			})
		},
	}
}

func renderChange(ci *gerrit.ChangeInfo) string {
	root := llmxml.NewElement("change",
		llmxml.Attr("number", ci.Number),
		llmxml.Attr("project", ci.Project),
		llmxml.Attr("branch", ci.Branch),
		llmxml.Attr("status", ci.Status),
		llmxml.Attr("owner", accountLabel(ci.Owner)),
		llmxml.Attr("created", timestamp(ci.Created)),
		llmxml.Attr("updated", timestamp(ci.Updated)),
	)

	if ci.Submittable {
		root.Attr(llmxml.Attr("submittable", true))
	}

	if ci.CurrentRevision != "" {
		root.Attr(llmxml.Attr("current_revision", ci.CurrentRevision))
	}

	children := []string{
		llmxml.NewElement("subject").InlineText(ci.Subject).String(),
	}

	if el := renderLabels(ci.Labels); el != "" {
		children = append(children, el)
	}

	if el := renderReviewers(ci.Reviewers); el != "" {
		children = append(children, el)
	}

	if el := renderMessages(ci.Messages); el != "" {
		children = append(children, el)
	}

	return root.WrapText(strings.Join(children, "\n")).String()
}

func renderLabels(labels map[string]gerrit.LabelInfo) string {
	if len(labels) == 0 {
		return ""
	}

	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}

	slices.Sort(names)

	rendered := make([]string, 0, len(names))

	for _, name := range names {
		info := labels[name]
		label := llmxml.NewElement("label", llmxml.Attr("name", name))

		if info.Optional {
			label.Attr(llmxml.Attr("optional", true))
		}

		votes := make([]string, 0, len(info.All))

		for _, approval := range info.All {
			if approval.Value == 0 {
				continue
			}

			votes = append(votes, llmxml.NewElement("vote",
				llmxml.Attr("value", approval.Value),
				llmxml.Attr("by", accountLabel(approval.AccountInfo)),
			).String())
		}

		if len(votes) > 0 {
			label.WrapText(strings.Join(votes, "\n"))
		}

		rendered = append(rendered, label.String())
	}

	return llmxml.NewElement("labels").WrapText(strings.Join(rendered, "\n")).String()
}

func renderReviewers(reviewers map[string][]gerrit.AccountInfo) string {
	if len(reviewers) == 0 {
		return ""
	}

	states := make([]string, 0, len(reviewers))
	for state := range reviewers {
		states = append(states, state)
	}

	slices.Sort(states)

	var rendered []string

	for _, state := range states {
		for _, account := range reviewers[state] {
			rendered = append(rendered, llmxml.NewElement("reviewer",
				llmxml.Attr("state", state),
			).InlineText(accountLabel(account)).String())
		}
	}

	return llmxml.NewElement("reviewers").WrapText(strings.Join(rendered, "\n")).String()
}

func renderMessages(messages []gerrit.ChangeMessageInfo) string {
	if len(messages) == 0 {
		return ""
	}

	rendered := make([]string, 0, len(messages))

	for _, msg := range messages {
		el := llmxml.NewElement("message",
			llmxml.Attr("author", accountLabel(msg.Author)),
			llmxml.Attr("date", timestamp(msg.Date)),
		)

		if msg.RevisionNumber > 0 {
			el.Attr(llmxml.Attr("revision", msg.RevisionNumber))
		}

		rendered = append(rendered, el.WrapText(msg.Message).String())
	}

	return llmxml.NewElement("messages").WrapText(strings.Join(rendered, "\n")).String()
}

// accountLabel renders an account as "Name (username)", degrading to
// whichever parts exist.
func accountLabel(a gerrit.AccountInfo) string {
	switch {
	case a.Name != "" && a.Username != "":
		return a.Name + " (" + a.Username + ")"
	case a.Name != "":
		return a.Name
	case a.Username != "":
		return a.Username
	default:
		return "unknown"
	}
}

func timestamp(t gerrit.Timestamp) string {
	return t.UTC().Format(time.RFC3339)
}
