package tools

import (
	"context"
	"slices"
	"strings"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

type listChangeFilesInput struct {
	Change   string `json:"change" jsonschema:"Change identifier: numeric ID, project~number, or Change-Id"`
	Revision string `json:"revision,omitempty" jsonschema:"Patch set number or SHA, defaults to current"`
}

func listChangeFiles(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameListChangeFiles,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameListChangeFiles,
				Description: "List the files of a Gerrit change revision with per-file change status " +
					"and inserted/deleted line counts.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in listChangeFilesInput) (*mcp.CallToolResult, any, error) {
				files, err := c.ListFiles(ctx, in.Change, in.Revision)
				if err != nil {
					return nil, nil, err
				}

				return textResult(renderFiles(in, files)), nil, nil
			})
		},
	}
}

func renderFiles(in listChangeFilesInput, files map[string]gerrit.FileInfo) string {
	root := llmxml.NewElement("files",
		llmxml.Attr("change", in.Change),
		llmxml.Attr("revision", revisionLabel(in.Revision)),
		llmxml.Attr("count", len(files)),
	)

	if len(files) == 0 {
		return root.String()
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	rendered := make([]string, 0, len(paths))

	for _, path := range paths {
		info := files[path]

		// Gerrit omits the status of plainly modified files.
		status := info.Status
		if status == "" {
			status = "M"
		}

		el := llmxml.NewElement("file",
			llmxml.Attr("path", path),
			llmxml.Attr("status", status),
			llmxml.Attr("insertions", info.LinesInserted),
			llmxml.Attr("deletions", info.LinesDeleted),
		)

		if info.Binary {
			el.Attr(llmxml.Attr("binary", true))
		}

		if info.OldPath != "" {
			el.Attr(llmxml.Attr("old_path", info.OldPath))
		}

		rendered = append(rendered, el.String())
	}

	return root.WrapText(strings.Join(rendered, "\n")).String()
}

func revisionLabel(revision string) string {
	if revision == "" {
		return gerritclient.CurrentRevision
	}

	return revision
}
