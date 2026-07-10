package tools

import (
	"context"
	"strings"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

const defaultSearchLimit = 25

var (
	errEmptyQuery = e.New("query must not be empty; start broad with e.g. status:open or owner:self")
	errNegStart   = e.New("start must be zero or positive")
)

type searchChangesInput struct {
	Query string `json:"query" jsonschema:"Gerrit change query, e.g. status:open owner:self; operators are listed in the tool description"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum results per page, default 25"`
	Start int    `json:"start,omitempty" jsonschema:"Results to skip; advance by limit while a page reports more=true"`
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
					"- for negation; quote multi-word values. Each result is a one-line change " +
					"summary; pass its number to get_change for the full review state. Paginate " +
					"via limit/start; more=\"true\" on the result signals further pages.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchChangesInput) (*mcp.CallToolResult, any, error) {
				if in.Limit <= 0 {
					in.Limit = defaultSearchLimit
				}

				if in.Start < 0 {
					return nil, nil, errNegStart.WithField("start", in.Start)
				}

				scope := c.Projects()

				// An empty query is a valid "everything in scope" browse when
				// project scoping supplies the clauses; without scope Gerrit
				// rejects it, so refuse before the round trip.
				if strings.TrimSpace(in.Query) == "" && len(scope) == 0 {
					return nil, nil, errEmptyQuery
				}

				res, err := c.QueryChanges(ctx, in.Query, in.Limit, in.Start)
				if err != nil {
					return nil, nil, err
				}

				return textResult(renderChangePage(in, res, scope)), nil, nil
			})
		},
	}
}

func renderChangePage(in searchChangesInput, res *gerritclient.QueryResult, scope []string) string {
	root := llmxml.NewElement("changes", llmxml.Attr("query", in.Query))

	// Scoping silently narrows every query; naming the scope here is what
	// tells the agent an empty page may mean "outside my scope", not "gone".
	if len(scope) > 0 {
		root.Attr(llmxml.Attr("scope", strings.Join(scope, ",")))
	}

	root.Attr(llmxml.Attr("start", in.Start)).
		Attr(llmxml.Attr("count", len(res.Changes))).
		Attr(llmxml.Attr("more", res.More))

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
