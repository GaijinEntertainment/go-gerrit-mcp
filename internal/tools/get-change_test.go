package tools_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

const selfJSON = ")]}'\n" + `{"_account_id":42,"name":"Review Bot","username":"bot"}`

const changeJSON = ")]}'\n" + `{
  "_number": 123,
  "project": "core",
  "branch": "main",
  "subject": "Fix nil deref in scanner",
  "status": "NEW",
  "created": "2026-07-01 10:00:00.000000000",
  "updated": "2026-07-02 11:30:00.000000000",
  "submittable": true,
  "current_revision": "abc123def",
  "owner": {"_account_id": 7, "name": "Alice", "username": "alice"},
  "labels": {
    "Code-Review": {
      "all": [
        {"_account_id": 8, "name": "Bob", "username": "bob", "value": 2},
        {"_account_id": 9, "name": "Carol", "username": "carol", "value": 0}
      ]
    }
  },
  "reviewers": {
    "REVIEWER": [{"_account_id": 8, "name": "Bob", "username": "bob"}]
  },
  "messages": [
    {"id": "m1", "author": {"_account_id": 8, "name": "Bob", "username": "bob"},
     "date": "2026-07-02 11:30:00.000000000", "message": "Looks good to me", "_revision_number": 2}
  ]
}`

// session spins a fake Gerrit plus an in-memory MCP server/client pair with
// the read-group tool registered, and returns the connected client session.
func session(t *testing.T, gerritHandler http.HandlerFunc) *mcp.ClientSession {
	t.Helper()

	srv := httptest.NewServer(gerritHandler)
	t.Cleanup(srv.Close)

	client, err := gerritclient.New(t.Context(), &config.Config{
		GerritURL: srv.URL,
		Username:  "bot",
		Token:     "s3cret",
		Groups:    []config.Group{config.GroupRead},
	})
	require.NoError(t, err)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	for _, tool := range tools.All(client) {
		tool.Register(mcpServer)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err = mcpServer.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)

	cs, err := mcpClient.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func Test_GetChange(t *testing.T) {
	t.Parallel()

	t.Run("lists all read tools", func(t *testing.T) {
		t.Parallel()

		cs := session(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(selfJSON))
		})

		res, err := cs.ListTools(t.Context(), nil)
		require.NoError(t, err)

		names := make([]string, 0, len(res.Tools))
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
		}

		assert.ElementsMatch(t, []string{
			"search_changes", "get_change", "list_change_files", "get_file_diff", "get_change_comments",
		}, names)
	})

	t.Run("renders change as llmxml", func(t *testing.T) {
		t.Parallel()

		var gotQuery string

		cs := session(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a/accounts/self" {
				_, _ = w.Write([]byte(selfJSON))

				return
			}

			gotQuery = r.URL.RawQuery

			_, _ = w.Write([]byte(changeJSON))
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "get_change",
			Arguments: map[string]any{"change": "123"},
		})
		require.NoError(t, err)
		require.False(t, res.IsError)

		require.Len(t, res.Content, 1)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)

		for _, part := range []string{
			`<change number="123"`,
			`project="core"`,
			`status="NEW"`,
			`owner="Alice (alice)"`,
			`submittable="true"`,
			`current_revision="abc123def"`,
			"<subject>Fix nil deref in scanner</subject>",
			`<label name="Code-Review">`,
			`<vote value="2" by="Bob (bob)"/>`,
			`<reviewer state="REVIEWER">Bob (bob)</reviewer>`,
			`<message author="Bob (bob)"`,
			"Looks good to me",
		} {
			assert.Contains(t, text.Text, part)
		}

		assert.NotContains(t, text.Text, "Carol", "zero votes must not render")

		wantOpts := []string{"DETAILED_LABELS", "DETAILED_ACCOUNTS", "CURRENT_REVISION", "MESSAGES", "SUBMITTABLE"}
		for _, opt := range wantOpts {
			assert.Contains(t, gotQuery, opt)
		}
	})

	t.Run("gerrit error surfaces message", func(t *testing.T) {
		t.Parallel()

		cs := session(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a/accounts/self" {
				_, _ = w.Write([]byte(selfJSON))

				return
			}

			w.WriteHeader(http.StatusNotFound)

			_, _ = w.Write([]byte("Not found: change 999"))
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "get_change",
			Arguments: map[string]any{"change": "999"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		require.NotEmpty(t, res.Content)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)

		assert.Contains(t, text.Text, "Not found: change 999")
		assert.Contains(t, text.Text, "404")
	})
}
