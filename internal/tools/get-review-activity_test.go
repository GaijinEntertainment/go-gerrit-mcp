package tools_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	activityQueryJSON = ")]}'\n" + `[{"_number":123,"project":"core","status":"NEW",` +
		`"updated":"2026-07-02 11:30:00.000000000"}]`

	activityDetailJSON = ")]}'\n" + `{"_number":123,"project":"core","status":"NEW",` +
		`"updated":"2026-07-02 11:30:00.000000000",` +
		`"messages":[` +
		`{"id":"m1","author":{"_account_id":8,"username":"bob"},"date":"2026-07-01 09:00:00.000000000",` +
		`"message":"old news","_revision_number":1},` +
		`{"id":"m2","author":{"_account_id":8,"username":"bob"},"date":"2026-07-02 11:30:00.000000000",` +
		`"message":"please rename the helper","_revision_number":2}]}`
)

// activityGerrit serves the endpoints that get_review_activity reads for
// change 123 and counts the detail fetches. A detailCode other than zero
// answers the detail fetch with that status and lists no change. A status
// other than empty replaces NEW in the query and the detail.
type activityGerrit struct {
	detailCode int
	status     string       `exhaustruct:"optional"`
	details    atomic.Int64 `exhaustruct:"optional"`
}

func (g *activityGerrit) change(body string) []byte {
	if g.status != "" {
		body = strings.ReplaceAll(body, `"status":"NEW"`, `"status":"`+g.status+`"`)
	}

	return []byte(body)
}

func (g *activityGerrit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/a/accounts/self":
		_, _ = w.Write([]byte(selfJSON))

	case "/a/changes/":
		if g.detailCode != 0 {
			_, _ = w.Write([]byte(")]}'\n[]"))

			return
		}

		_, _ = w.Write(g.change(activityQueryJSON))

	case "/a/changes/123":
		g.details.Add(1)

		if g.detailCode != 0 {
			w.WriteHeader(g.detailCode)

			_, _ = w.Write([]byte("Not found: change 123"))

			return
		}

		_, _ = w.Write(g.change(activityDetailJSON))

	case "/a/changes/123/comments":
		_, _ = w.Write([]byte(")]}'\n{}"))

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type activityEntry struct {
	Change  int    `json:"change"`
	Since   string `json:"since"`
	Status  string `json:"status"`
	Ended   bool   `json:"ended"`
	Summary string `json:"summary"`
	Content string `json:"content"`
	Error   string `json:"error"`
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	require.NotEmpty(t, res.Content)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	return text.Text
}

// callActivity calls get_review_activity for change 123 and returns its one
// entry, decoded from the text content.
func callActivity(t *testing.T, cs *mcp.ClientSession, request map[string]any) activityEntry {
	t.Helper()

	request["change"] = 123

	text := callTool(t, cs, "get_review_activity", map[string]any{"changes": []any{request}})

	var out struct {
		Changes []activityEntry `json:"changes"`
	}

	require.NoError(t, json.Unmarshal([]byte(text), &out))
	require.Len(t, out.Changes, 1)

	return out.Changes[0]
}

func Test_GetReviewActivity(t *testing.T) {
	t.Parallel()

	t.Run("no since returns the starting point and no activity", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: 0}
		cs := session(t, gerrit.ServeHTTP)

		got := callActivity(t, cs, map[string]any{})

		assert.Equal(t, activityEntry{
			Change: 123, Since: "2026-07-02T11:30:00Z", Status: "NEW",
			Ended: false, Summary: "", Content: "", Error: "",
		}, got)
		assert.Zero(t, gerrit.details.Load())
	})

	t.Run("activity past since is rendered and since advances", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: 0}
		cs := session(t, gerrit.ServeHTTP)

		got := callActivity(t, cs, map[string]any{"since": "2026-07-01T10:00:00Z", "status": "NEW"})

		assert.Equal(t, "2026-07-02T11:30:00Z", got.Since)
		assert.False(t, got.Ended)
		assert.Contains(t, got.Content, `<review_activity change="123"`)
		assert.Contains(t, got.Content, "please rename the helper")
		assert.NotContains(t, got.Content, "old news")
		assert.Equal(t, "1 message", got.Summary)
	})

	t.Run("a merge is a transition that ends the change", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: 0, status: "MERGED"}
		cs := session(t, gerrit.ServeHTTP)

		got := callActivity(t, cs, map[string]any{"since": "2026-07-01T10:00:00Z", "status": "NEW"})

		assert.Equal(t, "MERGED", got.Status)
		assert.True(t, got.Ended)
		assert.Contains(t, got.Content, `<transition from="NEW" to="MERGED"`)
		assert.Equal(t, "1 message, NEW -> MERGED", got.Summary)
	})

	t.Run("a change that did not move costs no detail fetch", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: 0}
		cs := session(t, gerrit.ServeHTTP)

		got := callActivity(t, cs, map[string]any{"since": "2026-07-02T11:30:00Z", "status": "NEW"})

		assert.Equal(t, activityEntry{
			Change: 123, Since: "2026-07-02T11:30:00Z", Status: "NEW",
			Ended: false, Summary: "", Content: "", Error: "",
		}, got)
		assert.Zero(t, gerrit.details.Load())
	})

	t.Run("a change that is no longer readable ends", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: http.StatusNotFound}
		cs := session(t, gerrit.ServeHTTP)

		got := callActivity(t, cs, map[string]any{"since": "2026-07-01T10:00:00Z", "status": "NEW"})

		assert.True(t, got.Ended)
		assert.Contains(t, got.Content, "<inaccessible>the change was deleted or is no longer visible")
		assert.Equal(t, "the change was deleted or is no longer visible to this account", got.Summary)
		assert.Equal(t, "2026-07-01T10:00:00Z", got.Since)
	})

	t.Run("a transient fetch failure is reported and keeps since", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: http.StatusBadGateway}
		cs := session(t, gerrit.ServeHTTP)

		got := callActivity(t, cs, map[string]any{"since": "2026-07-01T10:00:00Z", "status": "NEW"})

		assert.NotEmpty(t, got.Error)
		assert.False(t, got.Ended)
		assert.Empty(t, got.Content)
		assert.Equal(t, "2026-07-01T10:00:00Z", got.Since)
	})

	t.Run("since without status is refused", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: 0}
		cs := session(t, gerrit.ServeHTTP)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "get_review_activity",
			Arguments: map[string]any{
				"changes": []any{map[string]any{"change": 123, "since": "2026-07-01T10:00:00Z"}},
			},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Contains(t, resultText(t, res), "status is required together with since")
	})

	t.Run("the result is JSON text only", func(t *testing.T) {
		t.Parallel()

		gerrit := &activityGerrit{detailCode: 0}
		cs := session(t, gerrit.ServeHTTP)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "get_review_activity",
			Arguments: map[string]any{"changes": []any{map[string]any{"change": 123}}},
		})
		require.NoError(t, err)
		require.False(t, res.IsError)

		assert.Nil(t, res.StructuredContent)

		var out struct {
			Changes []map[string]any `json:"changes"`
		}

		require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
		require.Len(t, out.Changes, 1)

		url, _ := out.Changes[0]["url"].(string)
		assert.Regexp(t, `^http://127\.0\.0\.1:\d+/c/123$`, url)

		delete(out.Changes[0], "url")
		assert.Equal(t, map[string]any{"change": float64(123), "since": "2026-07-02T11:30:00Z", "status": "NEW"},
			out.Changes[0])
	})
}
