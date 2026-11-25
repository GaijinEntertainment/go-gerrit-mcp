package gerritclient_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_Learning_GoGerritErrorBody pins the go-gerrit contract the wrapper's
// error enrichment is built on: on a non-2xx response the library returns the
// wrapped *Response alongside the error WITHOUT reading or closing the body,
// so the caller can still read Gerrit's message from it. If an upgrade breaks
// this test, apiError silently stops carrying Gerrit messages — redesign it.
func Test_Learning_GoGerritErrorBody(t *testing.T) {
	t.Parallel()

	const gerritMessage = "change is not submittable: blocked by Code-Review"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)

		_, _ = w.Write([]byte(gerritMessage))
	}))
	t.Cleanup(srv.Close)

	client, err := gerrit.NewClient(t.Context(), srv.URL, nil)
	require.NoError(t, err)

	_, resp, err := client.Changes.GetChange(t.Context(), "123", nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "409")

	require.NotNil(t, resp)
	require.NotNil(t, resp.Body)

	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, gerritMessage, string(body))
}

// Test_Learning_GoGerritResultOnSuccess pins the go-gerrit contract that the
// wrapper dereferences without a nil check: a call that returns no error
// returns a non-nil result, even when Gerrit answers with a JSON null.
func Test_Learning_GoGerritResultOnSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(")]}'\nnull"))
	}))
	t.Cleanup(srv.Close)

	client, err := gerrit.NewClient(t.Context(), srv.URL, nil)
	require.NoError(t, err)

	tests := []struct {
		name string
		call func(t *testing.T) any
	}{
		{name: "GetAccount", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Accounts.GetAccount(t.Context(), "self")
			require.NoError(t, err)

			return v
		}},
		{name: "GetChange", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.GetChange(t.Context(), "123", nil)
			require.NoError(t, err)

			return v
		}},
		{name: "QueryChanges", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.QueryChanges(t.Context(), &gerrit.QueryChangeOptions{})
			require.NoError(t, err)

			return v
		}},
		{name: "GetDiff", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.GetDiff(t.Context(), "123", "current", "main.go", nil)
			require.NoError(t, err)

			return v
		}},
		{name: "ListChangeComments", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.ListChangeComments(t.Context(), "123")
			require.NoError(t, err)

			return v
		}},
		{name: "ListChangeDrafts", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.ListChangeDrafts(t.Context(), "123")
			require.NoError(t, err)

			return v
		}},
		{name: "SetReview", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.SetReview(t.Context(), "123", "current", &gerrit.ReviewInput{})
			require.NoError(t, err)

			return v
		}},
		{name: "SubmitChange", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.SubmitChange(t.Context(), "123", &gerrit.SubmitInput{})
			require.NoError(t, err)

			return v
		}},
		{name: "AbandonChange", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.AbandonChange(t.Context(), "123", &gerrit.AbandonInput{})
			require.NoError(t, err)

			return v
		}},
		{name: "RestoreChange", call: func(t *testing.T) any {
			t.Helper()

			v, _, err := client.Changes.RestoreChange(t.Context(), "123", &gerrit.RestoreInput{})
			require.NoError(t, err)

			return v
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NotNil(t, tt.call(t))
		})
	}
}
