package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

const stubSelfJSON = ")]}'\n" + `{"_account_id":42,"name":"Review Bot","username":"bot"}`

// readGroupTools is the zero-config tool list.
func readGroupTools() []string {
	return []string{
		"search_changes", "get_change", "list_change_files", "get_file_diff", "get_change_comments",
		"get_review_activity",
	}
}

func testGerritClient(t *testing.T, handler http.HandlerFunc) *gerritclient.Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := gerritclient.New(t.Context(), &config.Config{
		GerritURL:               srv.URL,
		Username:                "bot",
		Token:                   "s3cret",
		Groups:                  []config.Group{config.GroupRead},
		IncludeTools:            nil,
		ExcludeTools:            nil,
		Projects:                nil,
		AllowForeignChanges:     false,
		ActivityIncludeOwn:      false,
		ActivityExcludeAccounts: nil,
		ActivityExcludePatterns: nil,
	})
	require.NoError(t, err)

	return client
}

func testConfig() *config.Config {
	return &config.Config{
		GerritURL:               "",
		Username:                "",
		Token:                   "",
		Groups:                  []config.Group{config.GroupRead},
		IncludeTools:            nil,
		ExcludeTools:            nil,
		Projects:                nil,
		AllowForeignChanges:     false,
		ActivityIncludeOwn:      false,
		ActivityExcludeAccounts: nil,
		ActivityExcludePatterns: nil,
	}
}

func selfOnlyHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(stubSelfJSON))
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	return names
}

func Test_Assemble(t *testing.T) {
	t.Parallel()

	client := testGerritClient(t, selfOnlyHandler)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	s, err := assemble(testConfig(), client, serverTransport)
	require.NoError(t, err)

	_, err = s.mcp.Connect(t.Context(), s.transport, nil)
	require.NoError(t, err)

	session := connectClient(t, clientTransport)

	init := session.InitializeResult()
	assert.Equal(t, instructions, init.Instructions)
	assert.Empty(t, init.Capabilities.Experimental, "the server declares no experimental capability")

	assert.ElementsMatch(t, readGroupTools(), toolNames(t, session))
}

func connectClient(t *testing.T, transport mcp.Transport) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	session, err := client.Connect(t.Context(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}
