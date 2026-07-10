package gerritclient_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

// transitionCalls maps every transition operation to a uniform closure so the
// endpoint and gating tables below can iterate over them.
func transitionCalls() []struct {
	name     string
	call     func(t *testing.T, c *gerritclient.Client) error
	wantPath string
} {
	return []struct {
		name     string
		call     func(t *testing.T, c *gerritclient.Client) error
		wantPath string
	}{
		{
			name: "submit",
			call: func(t *testing.T, c *gerritclient.Client) error {
				t.Helper()

				_, err := c.SubmitChange(t.Context(), "123")

				return err
			},
			wantPath: "/a/changes/123/submit",
		},
		{
			name: "abandon",
			call: func(t *testing.T, c *gerritclient.Client) error {
				t.Helper()

				_, err := c.AbandonChange(t.Context(), "123", "stale")

				return err
			},
			wantPath: "/a/changes/123/abandon",
		},
		{
			name: "restore",
			call: func(t *testing.T, c *gerritclient.Client) error {
				t.Helper()

				_, err := c.RestoreChange(t.Context(), "123", "back")

				return err
			},
			wantPath: "/a/changes/123/restore",
		},
		{
			name: "wip",
			call: func(t *testing.T, c *gerritclient.Client) error {
				t.Helper()

				return c.SetWorkInProgress(t.Context(), "123", "parking")
			},
			wantPath: "/a/changes/123/revisions/current/review",
		},
		{
			name: "ready",
			call: func(t *testing.T, c *gerritclient.Client) error {
				t.Helper()

				return c.SetReadyForReview(t.Context(), "123", "ptal")
			},
			wantPath: "/a/changes/123/ready",
		},
	}
}

func Test_Transitions_Endpoints(t *testing.T) {
	t.Parallel()

	for _, tt := range transitionCalls() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, posts := reviewClient(t, testConfig(""), 42)

			require.NoError(t, tt.call(t, client))

			require.Len(t, *posts, 1)
			assert.Equal(t, tt.wantPath, (*posts)[0])
		})
	}
}

func Test_Transitions_Gating(t *testing.T) {
	t.Parallel()

	for _, tt := range transitionCalls() {
		t.Run(tt.name+" refused on foreign change", func(t *testing.T) {
			t.Parallel()

			client, posts := reviewClient(t, testConfig(""), 7)

			err := tt.call(t, client)

			require.Error(t, err)
			require.ErrorIs(t, err, gerritclient.ErrOwnChangesOnly)
			assert.Empty(t, *posts, "no mutating request may leave the process")
		})
	}
}

// Test_Transitions_ConflictSurfaced pins that a Gerrit refusal (409 with the
// reason in the body) reaches the caller verbatim — the agent must see why a
// submit was blocked.
func Test_Transitions_ConflictSurfaced(t *testing.T) {
	t.Parallel()

	changeJSON := ")]}'\n" + `{"_number":123,"project":"core","branch":"main",` +
		`"owner":{"_account_id":42,"username":"bot"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(scopedSelfJSON))

		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusConflict)

			_, _ = w.Write([]byte("Failed to submit 1 change due to the following problems:\n" +
				"Change 123: submit requirement 'Code-Review' is unsatisfied."))

		default:
			_, _ = w.Write([]byte(changeJSON))
		}
	}))
	t.Cleanup(srv.Close)

	cfg := testConfig("")

	cfg.GerritURL = srv.URL

	client, err := gerritclient.New(t.Context(), cfg)
	require.NoError(t, err)

	_, err = client.SubmitChange(t.Context(), "123")

	require.Error(t, err)
	require.ErrorIs(t, err, gerritclient.ErrSubmitChange)
	assert.ErrorContains(t, err, "submit requirement 'Code-Review' is unsatisfied")
}
