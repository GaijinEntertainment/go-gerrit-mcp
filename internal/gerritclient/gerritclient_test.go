package gerritclient_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

func testConfig(url string) *config.Config {
	return &config.Config{
		GerritURL:                       url,
		Username:                        "bot",
		Token:                           "s3cret",
		Groups:                          []config.Group{config.GroupRead},
		IncludeTools:                    nil,
		ExcludeTools:                    nil,
		Projects:                        nil,
		AllowForeignChanges:             false,
		ReviewNotifications:             false,
		ReviewNotificationsPollInterval: 0,
	}
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func Test_New(t *testing.T) {
	t.Parallel()

	t.Run("validates credentials against self account", func(t *testing.T) {
		t.Parallel()

		var gotPath, gotAuth string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")

			w.Header().Set("Content-Type", "application/json")

			_, _ = w.Write([]byte(")]}'\n{\"_account_id\":42,\"name\":\"Review Bot\",\"username\":\"bot\"}"))
		}))
		t.Cleanup(srv.Close)

		client, err := gerritclient.New(t.Context(), testConfig(srv.URL))
		require.NoError(t, err)

		assert.Equal(t, "/a/accounts/self", gotPath)
		assert.Equal(t, basicAuth("bot", "s3cret"), gotAuth)

		self := client.Self()
		assert.Equal(t, 42, self.AccountID)
		assert.Equal(t, "bot", self.Username)
	})

	t.Run("bad credentials surface gerrit message", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)

			_, _ = w.Write([]byte("Unauthorized: token expired"))
		}))
		t.Cleanup(srv.Close)

		_, err := gerritclient.New(t.Context(), testConfig(srv.URL))

		require.Error(t, err)
		assert.ErrorContains(t, err, "credential validation")
		assert.ErrorContains(t, err, "401")
		assert.ErrorContains(t, err, "token expired")
	})

	t.Run("invalid url fails client construction", func(t *testing.T) {
		t.Parallel()

		_, err := gerritclient.New(t.Context(), testConfig(""))

		require.Error(t, err)
		assert.ErrorContains(t, err, "create gerrit client")
	})
}
