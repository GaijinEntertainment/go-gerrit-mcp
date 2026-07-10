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

const ownChangeJSON = ")]}'\n" + `{"_number":123,"project":"core","branch":"main",` +
	`"owner":{"_account_id":42,"username":"bot"}}`

const existingCommentsJSON = ")]}'\n" + `{"core/scanner.go":[` +
	`{"id":"c1","line":10,"message":"root","updated":"2026-07-01 10:00:00.000000000"}]}`

// postSession wires a fake Gerrit for the comment flow: self-check, change
// resolve (owned by self), comments listing, and a recording review POST.
func postSession(t *testing.T) (cs *mcp.ClientSession, body *map[string]any) {
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

		case strings.HasSuffix(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(existingCommentsJSON))
		default:
			_, _ = w.Write([]byte(ownChangeJSON))
		}
	})

	return cs, body
}

func Test_PostComments(t *testing.T) {
	t.Parallel()

	t.Run("posts message, comments, replies, and resolution", func(t *testing.T) {
		t.Parallel()

		cs, body := postSession(t)

		out := callTool(t, cs, "post_comments", map[string]any{
			"change":  "123",
			"message": "Overall: looks solid",
			"notify":  "owner",
			"comments": []map[string]any{
				{"file": "core/scanner.go", "line": 10, "message": "Fixed", "reply_to": "c1", "resolved": true},
				{"file": "core/scanner.go", "start_line": 20, "end_line": 25, "message": "This block races"},
				{"file": "docs/readme.md", "message": "File-level note"},
			},
		})

		assert.Equal(t, `<review_posted change="123" message="true" comments="3" notify="OWNER"/>`, out)

		assert.Equal(t, "Overall: looks solid", (*body)["message"])
		assert.Equal(t, "OWNER", (*body)["notify"])

		comments, ok := (*body)["comments"].(map[string]any)
		require.True(t, ok)

		scanner, ok := comments["core/scanner.go"].([]any)
		require.True(t, ok)
		require.Len(t, scanner, 2)

		reply, ok := scanner[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "c1", reply["in_reply_to"])
		assert.InDelta(t, float64(10), reply["line"], 0)
		assert.Equal(t, false, reply["unresolved"], "resolved intent inverts to unresolved=false")

		ranged, ok := scanner[1].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, map[string]any{
			"start_line": float64(20), "start_character": float64(0),
			"end_line": float64(25), "end_character": float64(0),
		}, ranged["range"])

		readme, ok := comments["docs/readme.md"].([]any)
		require.True(t, ok)
		require.Len(t, readme, 1)
	})

	t.Run("unknown reply target refused", func(t *testing.T) {
		t.Parallel()

		cs, body := postSession(t)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "post_comments",
			Arguments: map[string]any{
				"change": "123",
				"comments": []map[string]any{
					{"file": "core/scanner.go", "message": "reply", "reply_to": "nope"},
				},
			},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "nope")

		assert.Empty(t, *body, "no review may be posted")
	})

	t.Run("empty review refused", func(t *testing.T) {
		t.Parallel()

		cs, _ := postSession(t)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "post_comments",
			Arguments: map[string]any{"change": "123"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)
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
				_, _ = w.Write([]byte(")]}'\n{\"_number\":123,\"project\":\"core\"," +
					"\"owner\":{\"_account_id\":7,\"username\":\"alice\"}}"))
			}
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "post_comments",
			Arguments: map[string]any{"change": "123", "message": "hi"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "own-changes")

		assert.False(t, posted, "no mutating request may leave the process")
	})
}
