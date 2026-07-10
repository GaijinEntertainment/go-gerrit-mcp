package gerritclient_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

const scopedSelfJSON = ")]}'\n" + `{"_account_id":42,"name":"Review Bot","username":"bot"}`

// scopedClient builds a client with a project allowlist against a recording
// fake Gerrit. requests collects every API path hit besides the self check.
func scopedClient(t *testing.T, handler http.HandlerFunc) (*gerritclient.Client, *[]string) {
	t.Helper()

	var (
		mu       sync.Mutex
		requests []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a/accounts/self" {
			_, _ = w.Write([]byte(scopedSelfJSON))

			return
		}

		mu.Lock()

		requests = append(requests, r.URL.String())

		mu.Unlock()

		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg := testConfig(srv.URL)

	cfg.Projects = []string{"core", "infra"}

	client, err := gerritclient.New(t.Context(), cfg)
	require.NoError(t, err)

	return client, &requests
}

func Test_ProjectScoping(t *testing.T) {
	t.Parallel()

	t.Run("query gets the allowlist clause injected", func(t *testing.T) {
		t.Parallel()

		var gotQuery string

		client, _ := scopedClient(t, func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query().Get("q")

			_, _ = w.Write([]byte(")]}'\n[]"))
		})

		_, err := client.QueryChanges(t.Context(), "status:open project:secret", 10, 0)
		require.NoError(t, err)

		assert.Equal(t, "(project:core OR project:infra) (status:open project:secret)", gotQuery)
	})

	t.Run("empty query becomes the allowlist clause", func(t *testing.T) {
		t.Parallel()

		var gotQuery string

		client, _ := scopedClient(t, func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query().Get("q")

			_, _ = w.Write([]byte(")]}'\n[]"))
		})

		_, err := client.QueryChanges(t.Context(), "", 10, 0)
		require.NoError(t, err)

		assert.Equal(t, "(project:core OR project:infra)", gotQuery)
	})

	t.Run("get_change refuses out-of-scope project", func(t *testing.T) {
		t.Parallel()

		client, _ := scopedClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(")]}'\n{\"_number\":9,\"project\":\"secret\",\"branch\":\"main\"}"))
		})

		_, err := client.GetChange(t.Context(), "9")

		require.Error(t, err)
		require.ErrorIs(t, err, gerritclient.ErrProjectScope)
	})

	t.Run("scoped id refused without any request", func(t *testing.T) {
		t.Parallel()

		client, requests := scopedClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(")]}'\n{}"))
		})

		_, err := client.ListFiles(t.Context(), "secret~123", "")

		require.Error(t, err)
		require.ErrorIs(t, err, gerritclient.ErrProjectScope)
		assert.Empty(t, *requests, "no API request may leave the process")
	})

	t.Run("bare id resolves project then refuses", func(t *testing.T) {
		t.Parallel()

		client, requests := scopedClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/comments") {
				t.Error("comments endpoint must not be reached")
			}

			_, _ = w.Write([]byte(")]}'\n{\"_number\":9,\"project\":\"secret\",\"branch\":\"main\"}"))
		})

		_, err := client.ListChangeComments(t.Context(), "9")

		require.Error(t, err)
		require.ErrorIs(t, err, gerritclient.ErrProjectScope)
		require.Len(t, *requests, 1, "exactly the resolving fetch")
	})

	t.Run("in-scope scoped id proceeds directly", func(t *testing.T) {
		t.Parallel()

		client, requests := scopedClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(")]}'\n{}"))
		})

		files, err := client.ListFiles(t.Context(), "core~123", "")
		require.NoError(t, err)

		assert.NotNil(t, files)
		require.Len(t, *requests, 1)
		assert.Equal(t, "/a/changes/core~123/revisions/current/files/", (*requests)[0])
	})

	t.Run("unscoped client passes everything through", func(t *testing.T) {
		t.Parallel()

		var gotQuery string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a/accounts/self" {
				_, _ = w.Write([]byte(scopedSelfJSON))

				return
			}

			gotQuery = r.URL.Query().Get("q")

			_, _ = w.Write([]byte(")]}'\n[]"))
		}))
		t.Cleanup(srv.Close)

		client, err := gerritclient.New(t.Context(), testConfig(srv.URL))
		require.NoError(t, err)

		_, err = client.QueryChanges(t.Context(), "status:open", 10, 0)
		require.NoError(t, err)
		require.NotErrorIs(t, err, gerritclient.ErrProjectScope)

		assert.Equal(t, "status:open", gotQuery)
	})
}
