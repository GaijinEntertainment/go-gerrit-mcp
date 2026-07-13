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
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

const subscribeChangeJSON = ")]}'\n" + `{
  "_number": 123,
  "project": "core",
  "branch": "main",
  "subject": "Fix nil deref in scanner",
  "status": "NEW",
  "created": "2026-07-01 10:00:00.000000000",
  "updated": "2026-07-02 11:30:00.000000000",
  "current_revision": "abc123def",
  "revisions": {"abc123def": {"_number": 3}},
  "owner": {"_account_id": 7, "name": "Alice", "username": "alice"}
}`

// subscribeSession mirrors session() with both subscription tools registered
// over the given store instead of the group-gated set.
func subscribeSession(
	t *testing.T, gerritHandler http.HandlerFunc, store *notifications.Store, mutate ...func(*config.Config),
) *mcp.ClientSession {
	t.Helper()

	srv := httptest.NewServer(gerritHandler)
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		GerritURL:                          srv.URL,
		Username:                           "bot",
		Token:                              "s3cret",
		Groups:                             []config.Group{config.GroupRead},
		IncludeTools:                       nil,
		ExcludeTools:                       nil,
		Projects:                           nil,
		AllowForeignChanges:                false,
		ReviewNotifications:                true,
		ReviewNotificationsPollInterval:    0,
		ReviewNotificationsIncludeOwn:      false,
		ReviewNotificationsExcludeAccounts: nil,
		ReviewNotificationsExcludePatterns: nil,
	}
	for _, m := range mutate {
		m(cfg)
	}

	client, err := gerritclient.New(t.Context(), cfg)
	require.NoError(t, err)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcpServer.AddReceivingMiddleware(tools.WrapErrors)

	tools.SubscribeChange(client, store).Register(mcpServer)
	tools.UnsubscribeChange(client, store).Register(mcpServer)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err = mcpServer.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)

	cs, err := mcpClient.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func subscribeGerritHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/a/accounts/self" {
		_, _ = w.Write([]byte(selfJSON))

		return
	}

	_, _ = w.Write([]byte(subscribeChangeJSON))
}

func callSubscribe(t *testing.T, cs *mcp.ClientSession, change string) *mcp.CallToolResult {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "subscribe_change",
		Arguments: map[string]any{"change": change},
	})
	require.NoError(t, err)

	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	require.NotEmpty(t, res.Content)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	return text.Text
}

func Test_SubscribeChange(t *testing.T) {
	t.Parallel()

	t.Run("acknowledges with status and patch set and stores the subscription", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		cs := subscribeSession(t, subscribeGerritHandler, store)

		res := callSubscribe(t, cs, "123")
		require.False(t, res.IsError)

		golden(t, "subscribe-change", resultText(t, res))

		assert.Equal(t, []int{123}, store.Changes())
	})

	t.Run("duplicate subscription refused", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		cs := subscribeSession(t, subscribeGerritHandler, store)

		require.False(t, callSubscribe(t, cs, "123").IsError)

		res := callSubscribe(t, cs, "123")
		require.True(t, res.IsError)
		assert.Contains(t, resultText(t, res), "already subscribed")
	})

	t.Run("nonexistent change refused with the standard error shape", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		cs := subscribeSession(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a/accounts/self" {
				_, _ = w.Write([]byte(selfJSON))

				return
			}

			w.WriteHeader(http.StatusNotFound)

			_, _ = w.Write([]byte("Not found: change 999"))
		}, store)

		res := callSubscribe(t, cs, "999")
		require.True(t, res.IsError)

		text := resultText(t, res)
		assert.Contains(t, text, `<error tool="subscribe_change">`)
		assert.Contains(t, text, "Not found: change 999")
		assert.Empty(t, store.Changes())
	})

	t.Run("out-of-scope change refused", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		cs := subscribeSession(t, subscribeGerritHandler, store, func(cfg *config.Config) {
			cfg.Projects = []string{"other"}
		})

		res := callSubscribe(t, cs, "123")
		require.True(t, res.IsError)
		assert.Contains(t, resultText(t, res), "outside the configured project scope")
		assert.Empty(t, store.Changes())
	})
}
