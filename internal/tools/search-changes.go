package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

const defaultSearchLimit = 25

type searchChangesInput struct {
	Query string `json:"query" jsonschema:"Gerrit change query string"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum results per page, default 25"`
	Start int    `json:"start,omitempty" jsonschema:"Number of results to skip, for pagination"`
}

func searchChanges(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameSearchChanges,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameSearchChanges,
				Description: "Search Gerrit changes using the standard query syntax. " +
					"Common operators: status:open|merged|abandoned, owner:USERNAME or owner:self, " +
					"project:NAME, branch:NAME, topic:NAME, label:Code-Review=+2, message:TEXT, " +
					"file:PATH, age:2d, is:wip. Terms combine with AND; use OR for alternatives and " +
					"- for negation; quote multi-word values. Paginate via limit/start and the " +
					"more attribute of the result.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchChangesInput) (*mcp.CallToolResult, any, error) {
				if in.Limit <= 0 {
					in.Limit = defaultSearchLimit
				}

				res, err := c.QueryChanges(ctx, in.Query, in.Limit, in.Start)
				if err != nil {
					return nil, nil, err
				}

				return textResult(renderChangePage(in, res)), nil, nil
			})
		},
	}
}

func renderChangePage(in searchChangesInput, res *gerritclient.QueryResult) string {
	root := llmxml.NewElement("changes",
		llmxml.Attr("query", in.Query),
		llmxml.Attr("start", in.Start),
		llmxml.Attr("count", len(res.Changes)),
		llmxml.Attr("more", res.More),
	)

	if len(res.Changes) == 0 {
		return root.String()
	}

	rendered := make([]string, 0, len(res.Changes))

	for _, ci := range res.Changes {
		el := llmxml.NewElement("change",
			llmxml.Attr("number", ci.Number),
			llmxml.Attr("project", ci.Project),
			llmxml.Attr("branch", ci.Branch),
			llmxml.Attr("status", ci.Status),
			llmxml.Attr("owner", accountLabel(ci.Owner)),
			llmxml.Attr("updated", timestamp(ci.Updated)),
		)

		rendered = append(rendered, el.InlineText(ci.Subject).String())
	}

	return root.WrapText(strings.Join(rendered, "\n")).String()
}
