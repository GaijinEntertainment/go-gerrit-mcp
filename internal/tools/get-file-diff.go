package tools

import (
	"context"
	"fmt"
	"strings"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

type getFileDiffInput struct {
	Change   string `json:"change" jsonschema:"Change identifier: numeric ID, project~number, or Change-Id"`
	File     string `json:"file" jsonschema:"File path within the change; /COMMIT_MSG addresses the commit message"`
	Revision string `json:"revision,omitempty" jsonschema:"Patch set number or SHA, defaults to current"`
}

func getFileDiff(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameGetFileDiff,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameGetFileDiff,
				Description: "Fetch the diff of one file in a Gerrit change revision. Lines are " +
					"prefixed unified-diff style: space for context, - for deleted, + for added.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in getFileDiffInput) (*mcp.CallToolResult, any, error) {
				diff, err := c.GetDiff(ctx, in.Change, in.Revision, in.File)
				if err != nil {
					return nil, nil, err
				}

				return textResult(renderDiff(in, diff)), nil, nil
			})
		},
	}
}

func renderDiff(in getFileDiffInput, diff *gerrit.DiffInfo) string {
	root := llmxml.NewElement("diff",
		llmxml.Attr("change", in.Change),
		llmxml.Attr("revision", revisionLabel(in.Revision)),
		llmxml.Attr("file", in.File),
		llmxml.Attr("change_type", diff.ChangeType),
	)

	if diff.Binary {
		root.Attr(llmxml.Attr("binary", true))

		return root.String()
	}

	var b strings.Builder

	for _, content := range diff.Content {
		if content.Skip > 0 {
			fmt.Fprintf(&b, "... %d common lines skipped ...\n", content.Skip)
		}

		writePrefixed(&b, " ", content.AB)
		writePrefixed(&b, "-", content.A)
		writePrefixed(&b, "+", content.B)
	}

	body := strings.TrimSuffix(b.String(), "\n")
	if body == "" {
		return root.String()
	}

	return root.WrapText(body).String()
}

func writePrefixed(b *strings.Builder, prefix string, lines []string) {
	for _, line := range lines {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
}
