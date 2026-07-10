package gerritclient_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

// reviewClient builds a client against a fake Gerrit whose change 123 lives
// in project "core" and is owned by ownerID. posts collects review POSTs.
// The authenticated account id is 42 (scopedSelfJSON).
func reviewClient(t *testing.T, cfg *config.Config, ownerID int) (*gerritclient.Client, *[]string) {
	t.Helper()

	var (
		mu    sync.Mutex
		posts []string
	)

	changeJSON := ")]}'\n" + `{"_number":123,"project":"core","branch":"main",` +
		`"owner":{"_account_id":` + strconv.Itoa(ownerID) + `,"username":"owner"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(scopedSelfJSON))
		case r.Method == http.MethodPost:
			mu.Lock()

			posts = append(posts, r.URL.Path)

			mu.Unlock()

			_, _ = w.Write([]byte(")]}'\n{}"))

		default:
			_, _ = w.Write([]byte(changeJSON))
		}
	}))
	t.Cleanup(srv.Close)

	cfg.GerritURL = srv.URL

	client, err := gerritclient.New(t.Context(), cfg)
	require.NoError(t, err)

	return client, &posts
}

func reviewInput(msg string) *gerrit.ReviewInput {
	return &gerrit.ReviewInput{Message: msg}
}

func Test_SetReview_Gating(t *testing.T) {
	t.Parallel()

	t.Run("own change passes", func(t *testing.T) {
		t.Parallel()

		client, posts := reviewClient(t, testConfig(""), 42)

		_, err := client.SetReview(t.Context(), "123", "", reviewInput("lgtm"))
		require.NoError(t, err)

		require.Len(t, *posts, 1)
		assert.Equal(t, "/a/changes/123/revisions/current/review", (*posts)[0])
	})

	t.Run("foreign change refused by default", func(t *testing.T) {
		t.Parallel()

		client, posts := reviewClient(t, testConfig(""), 7)

		_, err := client.SetReview(t.Context(), "123", "", reviewInput("hi"))

		require.Error(t, err)
		require.ErrorIs(t, err, gerritclient.ErrOwnChangesOnly)
		assert.Empty(t, *posts, "no mutating request may leave the process")
	})

	t.Run("foreign change passes when restriction disabled", func(t *testing.T) {
		t.Parallel()

		cfg := testConfig("")

		cfg.AllowForeignChanges = true

		client, posts := reviewClient(t, cfg, 7)

		_, err := client.SetReview(t.Context(), "123", "", reviewInput("hi"))
		require.NoError(t, err)

		require.Len(t, *posts, 1)
	})

	t.Run("project scope refuses even with foreign allowed", func(t *testing.T) {
		t.Parallel()

		cfg := testConfig("")

		cfg.AllowForeignChanges = true
		cfg.Projects = []string{"infra"}

		client, posts := reviewClient(t, cfg, 7)

		_, err := client.SetReview(t.Context(), "123", "", reviewInput("hi"))

		require.Error(t, err)
		require.ErrorIs(t, err, gerritclient.ErrProjectScope)
		assert.Empty(t, *posts)
	})
}
