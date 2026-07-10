package tools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const foreignChangeJSON = ")]}'\n" + `{"_number":123,"project":"core","branch":"main",` +
	`"owner":{"_account_id":7,"username":"alice"}}`

// voteSession wires a fake Gerrit for the vote flow: self-check, change
// resolve (owned by self), and a recording review POST.
func voteSession(t *testing.T) (cs *mcp.ClientSession, body *map[string]any) {
	t.Helper()

	body = &map[string]any{}

	cs = session(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(selfJSON))

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/review"):
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read review body: %v", err)

				return
			}

			if err := json.Unmarshal(raw, body); err != nil {
				t.Errorf("unmarshal review body: %v", err)

				return
			}

			_, _ = w.Write([]byte(")]}'\n{}"))

		default:
			_, _ = w.Write([]byte(ownChangeJSON))
		}
	})

	return cs, body
}

func Test_SetVote(t *testing.T) {
	t.Parallel()

	t.Run("posts label, value, and message as a review", func(t *testing.T) {
		t.Parallel()

		cs, body := voteSession(t)

		out := callTool(t, cs, "set_vote", map[string]any{
			"change":  "123",
			"label":   "Code-Review",
			"value":   2,
			"message": "lgtm",
		})

		assert.Equal(t, `<vote_set change="123" label="Code-Review" value="2"/>`, out)

		assert.Equal(t, "lgtm", (*body)["message"])
		assert.Equal(t, map[string]any{"Code-Review": float64(2)}, (*body)["labels"])
	})

	t.Run("zero value stays on the wire to clear a vote", func(t *testing.T) {
		t.Parallel()

		cs, body := voteSession(t)

		callTool(t, cs, "set_vote", map[string]any{
			"change": "123",
			"label":  "Code-Review",
			"value":  0,
		})

		assert.Equal(t, map[string]any{"Code-Review": float64(0)}, (*body)["labels"])
	})

	t.Run("empty label refused", func(t *testing.T) {
		t.Parallel()

		cs, body := voteSession(t)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "set_vote",
			Arguments: map[string]any{"change": "123", "label": " ", "value": 1},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		assert.Empty(t, *body, "no review may be posted")
	})

	t.Run("gerrit label rejection surfaced verbatim", func(t *testing.T) {
		t.Parallel()

		cs := session(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/a/accounts/self":
				_, _ = w.Write([]byte(selfJSON))

			case r.Method == http.MethodPost:
				w.WriteHeader(http.StatusBadRequest)

				_, _ = w.Write([]byte(`label "Bogus" is not a configured label`))

			default:
				_, _ = w.Write([]byte(ownChangeJSON))
			}
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "set_vote",
			Arguments: map[string]any{"change": "123", "label": "Bogus", "value": 1},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, `label "Bogus" is not a configured label`)
	})

	t.Run("foreign change refused by own-changes restriction", func(t *testing.T) {
		t.Parallel()

		posted := false

		cs := session(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/a/accounts/self":
				_, _ = w.Write([]byte(selfJSON))

			case r.Method == http.MethodPost:
				posted = true

				_, _ = w.Write([]byte(")]}'\n{}"))

			default:
				_, _ = w.Write([]byte(foreignChangeJSON))
			}
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "set_vote",
			Arguments: map[string]any{"change": "123", "label": "Code-Review", "value": -1},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "own-changes")

		assert.False(t, posted, "no mutating request may leave the process")
	})
}
