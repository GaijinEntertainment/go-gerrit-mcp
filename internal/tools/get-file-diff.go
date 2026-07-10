package tools

import (
	"context"
	"fmt"
	"strings"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

var errFileNotInRevision = e.New("file is not part of the revision")

type getFileDiffInput struct {
	Change   string `json:"change" jsonschema:"Change identifier: change number (123), project~number (myproject~123), or Change-Id (I8473b95...)"`
	File     string `json:"file" jsonschema:"File path exactly as list_change_files reports it; /COMMIT_MSG for the commit message"`
	Revision string `json:"revision,omitempty" jsonschema:"Patch set number (1, 2, ...) or revision SHA; omit for the newest patch set"`
}

func getFileDiff(c *gerritclient.Client) Tool {
	return Tool{
		Name: NameGetFileDiff,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameGetFileDiff,
				Description: "Fetch the diff of one file in a Gerrit change revision. Lines are " +
					"prefixed unified-diff style: space for context, - for deleted, + for added; " +
					"a skip marker stands in for long unchanged stretches. Take file paths from " +
					"list_change_files; /COMMIT_MSG diffs the commit message.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in getFileDiffInput) (*mcp.CallToolResult, any, error) {
				diff, err := c.GetDiff(ctx, in.Change, in.Revision, in.File)
				if err != nil {
					return nil, nil, err
				}

				// Gerrit answers 200 with an empty diff for paths absent from
				// the revision; distinguish "no such file" from a legitimately
				// empty diff before rendering a misleading success.
				if !diff.Binary && len(diff.Content) == 0 {
					if err := checkDiffFile(ctx, c, in); err != nil {
						return nil, nil, err
					}
				}

				return textResult(renderDiff(in, diff)), nil, nil
			})
		},
	}
}

// checkDiffFile verifies the requested file exists in the revision, turning
// Gerrit's ambiguous empty diff into an error that carries the closest
// actual paths. Best-effort: an unverifiable file list keeps the empty diff.
func checkDiffFile(ctx context.Context, c *gerritclient.Client, in getFileDiffInput) error {
	files, err := c.ListFiles(ctx, in.Change, in.Revision)
	if err != nil {
		return nil //nolint:nilerr // verification is best-effort, the empty diff render stands
	}

	if _, ok := files[in.File]; ok {
		return nil
	}

	res := errFileNotInRevision.WithFields(
		fields.F("change", in.Change),
		fields.F("revision", revisionLabel(in.Revision)),
		fields.F("file", in.File),
	)

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}

	if near := proposals(in.File, paths); len(near) > 0 {
		return res.WithField("did_you_mean", strings.Join(near, ", "))
	}

	return res.WithField("hint", "list_change_files names the files of the revision")
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
