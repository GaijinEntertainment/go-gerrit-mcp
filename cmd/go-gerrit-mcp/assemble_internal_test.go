package main

import (
	"encoding/json"
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

func readyConn(client *gerritclient.Client) gerritConn {
	return gerritConn{client: client, state: tools.StateReady, reason: ""}
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

	s, err := assemble(testConfig(), readyConn(client), serverTransport)
	require.NoError(t, err)

	_, err = s.mcp.Connect(t.Context(), s.transport, nil)
	require.NoError(t, err)

	session := connectClient(t, clientTransport)

	init := session.InitializeResult()
	assert.Equal(t, instructions, init.Instructions)
	assert.Empty(t, init.Capabilities.Experimental, "the server declares no experimental capability")

	assert.ElementsMatch(t, append(readGroupTools(), "get_server_info"), toolNames(t, session))
}

func Test_GetServerInfo(t *testing.T) {
	t.Parallel()

	client := testGerritClient(t, selfOnlyHandler)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	cfg := testConfig()

	cfg.ExcludeTools = []string{"get_review_activity"}

	s, err := assemble(cfg, readyConn(client), serverTransport)
	require.NoError(t, err)

	_, err = s.mcp.Connect(t.Context(), s.transport, nil)
	require.NoError(t, err)

	session := connectClient(t, clientTransport)

	assert.Equal(t, map[string]any{
		"name":             "go-gerrit-mcp",
		"version":          serverVersion(),
		"protocol_version": session.InitializeResult().ProtocolVersion,
		"state":            "ready",
		"account":          "bot",
		"groups":           []any{"read"},
		"tools": []any{
			"search_changes", "get_change", "list_change_files", "get_file_diff", "get_change_comments",
		},
	}, serverInfo(t, session))
}

func Test_Assemble_NotReady(t *testing.T) {
	t.Parallel()

	conn := gerritConn{
		client: nil,
		state:  tools.StateCredentialsRejected,
		reason: "gerrit credential validation (status=401)",
	}

	t.Run("serves get_server_info only and names the cause", func(t *testing.T) {
		t.Parallel()

		serverTransport, clientTransport := mcp.NewInMemoryTransports()

		s, err := assemble(testConfig(), conn, serverTransport)
		require.NoError(t, err)

		_, err = s.mcp.Connect(t.Context(), s.transport, nil)
		require.NoError(t, err)

		session := connectClient(t, clientTransport)

		assert.Equal(t, notReadyInstructions+conn.reason, session.InitializeResult().Instructions)
		assert.Equal(t, []string{"get_server_info"}, toolNames(t, session))
		assert.Equal(t, map[string]any{
			"name":             "go-gerrit-mcp",
			"version":          serverVersion(),
			"protocol_version": session.InitializeResult().ProtocolVersion,
			"state":            "credentials_rejected",
			"reason":           conn.reason,
			"groups":           []any{"read"},
		}, serverInfo(t, session))
	})

	t.Run("an unknown tool name still fails", func(t *testing.T) {
		t.Parallel()

		cfg := testConfig()

		cfg.ExcludeTools = []string{"no_such_tool"}

		serverTransport, _ := mcp.NewInMemoryTransports()

		_, err := assemble(cfg, conn, serverTransport)
		require.ErrorContains(t, err, "no_such_tool")
	})
}

func Test_Connect(t *testing.T) {
	t.Parallel()

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	tests := []struct {
		name       string
		giveURL    string
		giveStatus int
		wantState  tools.ServerState
		wantReason string
	}{
		{
			name:       "accepted credentials",
			giveURL:    "",
			giveStatus: http.StatusOK,
			wantState:  tools.StateReady,
			wantReason: "^$",
		},
		{
			name:       "rejected credentials",
			giveURL:    "",
			giveStatus: http.StatusUnauthorized,
			wantState:  tools.StateCredentialsRejected,
			wantReason: "status=401",
		},
		{
			name:       "forbidden account",
			giveURL:    "",
			giveStatus: http.StatusForbidden,
			wantState:  tools.StateCredentialsRejected,
			wantReason: "status=403",
		},
		{
			name:       "server error",
			giveURL:    "",
			giveStatus: http.StatusBadGateway,
			wantState:  tools.StateUnreachable,
			wantReason: "status=502",
		},
		{
			name:       "nothing listens",
			giveURL:    closed.URL,
			giveStatus: 0,
			wantState:  tools.StateUnreachable,
			wantReason: "gerrit credential validation",
		},
		{
			name:       "unparseable URL",
			giveURL:    "://gerrit",
			giveStatus: 0,
			wantState:  tools.StateNotConfigured,
			wantReason: "create gerrit client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.giveStatus != http.StatusOK {
					w.WriteHeader(tt.giveStatus)

					return
				}

				_, _ = w.Write([]byte(stubSelfJSON))
			}))
			t.Cleanup(srv.Close)

			cfg := testConfig()

			cfg.GerritURL, cfg.Username, cfg.Token = srv.URL, "bot", "s3cret"
			if tt.giveURL != "" {
				cfg.GerritURL = tt.giveURL
			}

			conn := connect(t.Context(), cfg)

			assert.Equal(t, tt.wantState, conn.state)
			assert.Regexp(t, tt.wantReason, conn.reason)
			assert.Equal(t, tt.wantState == tools.StateReady, conn.client != nil)
		})
	}

	t.Run("unset variables", func(t *testing.T) {
		t.Parallel()

		cfg := testConfig()

		cfg.GerritURL = "https://gerrit.example.com"

		conn := connect(t.Context(), cfg)

		assert.Equal(t, tools.StateNotConfigured, conn.state)
		assert.Equal(t, "required environment variables are not set: GERRIT_USERNAME, GERRIT_TOKEN", conn.reason)
		assert.Nil(t, conn.client)
	})
}

func serverInfo(t *testing.T, session *mcp.ClientSession) map[string]any {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_server_info"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var got map[string]any

	require.NoError(t, json.Unmarshal([]byte(text.Text), &got))

	return got
}

func connectClient(t *testing.T, transport mcp.Transport) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	session, err := client.Connect(t.Context(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}
