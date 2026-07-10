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
