package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

// WrapErrors is receiving middleware that re-renders in-band tool-call errors
// as llmxml, so error output speaks the same dialect as success output
// (ADR 1.3). Placed on the server it covers every error class uniformly:
// SDK argument validation, client-side refusals, and Gerrit API passthrough.
// Protocol-level failures (e.g. unknown tool) are JSON-RPC errors and pass
// through untouched.
func WrapErrors(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil {
			return res, err
		}

		call, ok := req.(*mcp.CallToolRequest)
		if !ok {
			return res, nil
		}

		out, ok := res.(*mcp.CallToolResult)
		if !ok || !out.IsError || len(out.Content) != 1 {
			return res, nil
		}

		text, ok := out.Content[0].(*mcp.TextContent)
		if !ok {
			return res, nil
		}

		wrapped := llmxml.NewElement("error", llmxml.Attr("tool", call.Params.Name)).
			WrapText(text.Text).
			String()

		out.Content = []mcp.Content{&mcp.TextContent{Text: wrapped}}

		return out, nil
	}
}
