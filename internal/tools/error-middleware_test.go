package tools_test

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// errorText extracts the sole text content of an error result.
func errorText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	require.True(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	return text.Text
}

// Tool errors are a product contract like success renders (ADR 1.3): every
// in-band error class reaches the model wrapped as llmxml. Validation errors
// originate in the SDK before handler code runs; refusals originate in
// handlers — the middleware must cover both.
func Test_WrapErrors(t *testing.T) {
	t.Parallel()

	t.Run("argument validation error is llmxml", func(t *testing.T) {
		t.Parallel()

		cs, _ := fakeGerrit(t, "")

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "search_changes",
			Arguments: map[string]any{},
		})
		require.NoError(t, err)

		golden(t, "error-validation", errorText(t, res))
	})

	t.Run("handler refusal is llmxml", func(t *testing.T) {
		t.Parallel()

		cs, _ := transitionSession(t, ")]}'\n{}")

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "transition_change",
			Arguments: map[string]any{"change": "123", "action": "merge"},
		})
		require.NoError(t, err)

		golden(t, "error-refusal", errorText(t, res))
	})
}
