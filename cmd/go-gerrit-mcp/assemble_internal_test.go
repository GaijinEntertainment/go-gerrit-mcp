package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

const stubSelfJSON = ")]}'\n" + `{"_account_id":42,"name":"Review Bot","username":"bot"}`

// readGroupTools is the historical zero-config tool list; byte-identity of
// the disabled path is asserted against it.
func readGroupTools() []string {
	return []string{
		"search_changes", "get_change", "list_change_files", "get_file_diff", "get_change_comments",
	}
}

func testGerritClient(t *testing.T, handler http.HandlerFunc) *gerritclient.Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := gerritclient.New(t.Context(), &config.Config{
		GerritURL:                       srv.URL,
		Username:                        "bot",
		Token:                           "s3cret",
		Groups:                          []config.Group{config.GroupRead},
		IncludeTools:                    nil,
		ExcludeTools:                    nil,
		Projects:                        nil,
		AllowForeignChanges:             false,
		ReviewNotifications:             false,
		ReviewNotificationsPollInterval: 0,
	})
	require.NoError(t, err)

	return client
}

func testConfig(reviewNotifications bool, interval time.Duration) *config.Config {
	return &config.Config{
		GerritURL:                       "",
		Username:                        "",
		Token:                           "",
		Groups:                          []config.Group{config.GroupRead},
		IncludeTools:                    nil,
		ExcludeTools:                    nil,
		Projects:                        nil,
		AllowForeignChanges:             false,
		ReviewNotifications:             reviewNotifications,
		ReviewNotificationsPollInterval: interval,
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

func Test_Assemble_Disabled(t *testing.T) {
	t.Parallel()

	client := testGerritClient(t, selfOnlyHandler)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	s, err := assemble(testConfig(false, 0), client, serverTransport, discardLogger())
	require.NoError(t, err)
	assert.Nil(t, s.poller, "disabled feature must assemble no poller")
	assert.Same(t, serverTransport, s.transport, "disabled feature must not wrap the transport")

	_, err = s.mcp.Connect(t.Context(), s.transport, nil)
	require.NoError(t, err)

	session := connectClient(t, clientTransport)

	init := session.InitializeResult()
	assert.Equal(t, instructions, init.Instructions, "zero-config instructions must stay byte-identical")

	caps := init.Capabilities
	require.NotNil(t, caps)
	assert.NotNil(t, caps.Logging, "zero-config keeps the SDK logging default")
	assert.NotNil(t, caps.Tools)
	assert.Empty(t, caps.Experimental, "zero-config declares no experimental capability")

	assert.ElementsMatch(t, readGroupTools(), toolNames(t, session))
}

func Test_Assemble_Enabled(t *testing.T) {
	t.Parallel()

	client := testGerritClient(t, selfOnlyHandler)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	s, err := assemble(testConfig(true, time.Minute), client, serverTransport, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, s.poller)

	_, err = s.mcp.Connect(t.Context(), s.transport, nil)
	require.NoError(t, err)

	session := connectClient(t, clientTransport)

	init := session.InitializeResult()
	assert.Equal(t, instructions+notificationsInstructions, init.Instructions)

	caps := init.Capabilities
	require.NotNil(t, caps)
	assert.Contains(t, caps.Experimental, channelCapability)
	assert.NotNil(t, caps.Logging, "logging capability must survive the override")
	assert.NotNil(t, caps.Tools, "tools capability must survive the override")

	assert.ElementsMatch(t, append(readGroupTools(), "subscribe_change"), toolNames(t, session))
}

// Test_Assemble_TracerBullet drives the whole enabled stack in-process:
// subscribe through the tool, move the change on the stub Gerrit, receive
// the channel notification pushed by the poller.
func Test_Assemble_TracerBullet(t *testing.T) {
	t.Parallel()

	const receiveTimeout = 10 * time.Second

	fetched := ")]}'\n" + `{"_number":123,"project":"core","status":"NEW",` +
		`"updated":"2026-07-01 10:00:00.000000000",` +
		`"current_revision":"abc","revisions":{"abc":{"_number":1}}}`
	moved := ")]}'\n" + `[{"_number":123,"project":"core","status":"NEW",` +
		`"updated":"2026-07-01 11:00:00.000000000"}]`

	client := testGerritClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(stubSelfJSON))
		case strings.HasPrefix(r.URL.Path, "/a/changes/123"):
			_, _ = w.Write([]byte(fetched))
		default:
			_, _ = w.Write([]byte(moved))
		}
	})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	observed := &observedTransport{
		inner:         clientTransport,
		notifications: make(chan *jsonrpc.Request, 16),
	}

	s, err := assemble(testConfig(true, 10*time.Millisecond), client, serverTransport, discardLogger())
	require.NoError(t, err)

	runDone := make(chan error, 1)

	go func() { runDone <- s.run(t.Context()) }()

	session := connectClient(t, observed)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "subscribe_change",
		Arguments: map[string]any{"change": "123"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	select {
	case req := <-observed.notifications:
		assert.Equal(t, channelMethod, req.Method)

		var params channelParams

		require.NoError(t, json.Unmarshal(req.Params, &params))
		assert.Contains(t, params.Content, `<review_activity change="123"`)
		assert.Equal(t, map[string]string{"change": "123"}, params.Meta)

	case <-time.After(receiveTimeout):
		t.Fatal("no channel notification before timeout")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func connectClient(t *testing.T, transport mcp.Transport) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	session, err := client.Connect(t.Context(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}
