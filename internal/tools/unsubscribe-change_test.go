package tools_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

func callUnsubscribe(t *testing.T, cs *mcp.ClientSession, change string) *mcp.CallToolResult {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "unsubscribe_change",
		Arguments: map[string]any{"change": change},
	})
	require.NoError(t, err)

	return res
}

func Test_UnsubscribeChange(t *testing.T) {
	t.Parallel()

	seed := notifications.NewCursor(time.Date(2026, 7, 2, 11, 30, 0, 0, time.UTC), "NEW")

	t.Run("ends a subscription and acknowledges", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		store.Add(123, seed)

		cs := subscribeSession(t, subscribeGerritHandler, store)

		res := callUnsubscribe(t, cs, "123")
		require.False(t, res.IsError)

		golden(t, "unsubscribe-change", resultText(t, res))
		assert.Empty(t, store.Changes())
	})

	t.Run("tolerates a change that was not subscribed", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		cs := subscribeSession(t, subscribeGerritHandler, store)

		res := callUnsubscribe(t, cs, "123")
		require.False(t, res.IsError)

		text := resultText(t, res)
		assert.Contains(t, text, `was_subscribed="false"`)
		assert.Contains(t, text, "was not subscribed")
	})

	t.Run("numeric identifier needs no fetch", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		store.Add(123, seed)

		cs := subscribeSession(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a/accounts/self" {
				_, _ = w.Write([]byte(selfJSON))

				return
			}

			w.WriteHeader(http.StatusNotFound)
		}, store)

		res := callUnsubscribe(t, cs, "123")
		require.False(t, res.IsError, "unsubscribing must not depend on the change still being fetchable")
		assert.Empty(t, store.Changes())
	})

	t.Run("non-numeric identifier resolves through the fetch", func(t *testing.T) {
		t.Parallel()

		store := notifications.NewStore()
		store.Add(123, seed)

		cs := subscribeSession(t, subscribeGerritHandler, store)

		res := callUnsubscribe(t, cs, "core~123")
		require.False(t, res.IsError)
		assert.Empty(t, store.Changes())
	})
}
