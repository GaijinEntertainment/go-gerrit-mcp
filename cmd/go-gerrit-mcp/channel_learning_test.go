package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const channelMethod = "notifications/claude/channel"

// Test_Learning_CapabilitiesOverride pins the go-sdk contract the channel
// capability declaration is built on: setting ServerOptions.Capabilities to a
// non-nil value with only an Experimental entry does NOT lose the inferred
// tools capability — the SDK still augments Tools when tools are registered —
// but it DOES drop the {"logging":{}} default that a nil Capabilities carries.
// If an upgrade breaks the tools half, the conditional capability wiring must
// set Tools explicitly; the logging half means the wiring must always set
// Logging alongside Experimental to keep capability parity with the disabled
// path.
func Test_Learning_CapabilitiesOverride(t *testing.T) {
	t.Parallel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	srv := mcp.NewServer(
		&mcp.Implementation{Name: "learning", Version: "0"},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{
				Experimental: map[string]any{"claude/channel": map[string]any{}},
			},
		},
	)
	registerEchoTool(srv)

	_, err := srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	caps := session.InitializeResult().Capabilities
	require.NotNil(t, caps)

	assert.NotNil(t, caps.Tools, "tools capability must survive the Experimental override")
	assert.Contains(t, caps.Experimental, "claude/channel")
	assert.Nil(t, caps.Logging, "overriding Capabilities drops the logging default — declare it explicitly")
}

// Test_Learning_RawNotificationWrite pins the transport contract the channel
// emitter is built on: an ID-less jsonrpc.Request written directly to the
// mcp.Connection captured at Connect is framed as a JSON-RPC notification and
// received intact by the peer, even when the writes race SDK-originated tool
// responses on the same connection (Connection.Write is documented
// concurrency-safe). The capturing transport returns the inner connection
// unwrapped: the SDK type-asserts it to an unexported interface for session
// state updates, so a proxying wrapper would silently change behavior. A torn
// or misframed message would kill the session and fail the concurrent tool
// calls below.
func Test_Learning_RawNotificationWrite(t *testing.T) {
	t.Parallel()

	const (
		writers              = 2
		notificationsPerSide = 25
		callers              = 4
		callsPerCaller       = 10
	)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	capture := &captureTransport{inner: serverTransport}
	tee := &teeTransport{
		inner:         clientTransport,
		notifications: make(chan *jsonrpc.Request, writers*notificationsPerSide),
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "learning", Version: "0"}, nil)
	registerEchoTool(srv)

	_, err := srv.Connect(t.Context(), capture, nil)
	require.NoError(t, err)
	require.NotNil(t, capture.conn)

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	session, err := client.Connect(t.Context(), tee, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	var wg sync.WaitGroup

	for w := range writers {
		wg.Go(func() { emitChannelNotifications(t, capture.conn, w, notificationsPerSide) })
	}

	for range callers {
		wg.Go(func() { callEchoRepeatedly(t, session, callsPerCaller) })
	}

	wg.Wait()

	received := drainNotifications(t, tee.notifications, writers*notificationsPerSide)

	for w := range writers {
		for i := range notificationsPerSide {
			assert.True(t, received[fmt.Sprintf("event-%d-%d", w, i)], "notification %d-%d lost", w, i)
		}
	}
}

// emitChannelNotifications writes count ID-less channel notifications through
// the captured server connection, each with a content marker unique to the
// writer.
func emitChannelNotifications(t *testing.T, conn mcp.Connection, writer, count int) {
	t.Helper()

	for i := range count {
		params, err := json.Marshal(map[string]any{
			"content": fmt.Sprintf("event-%d-%d", writer, i),
			"meta":    map[string]string{"change": "42"},
		})
		if err != nil {
			t.Errorf("marshal params: %v", err)

			return
		}

		// A zero ID is what makes this request a JSON-RPC notification.
		req := &jsonrpc.Request{ID: jsonrpc.ID{}, Method: channelMethod, Params: params, Extra: nil}
		if err := conn.Write(t.Context(), req); err != nil {
			t.Errorf("raw write %d-%d: %v", writer, i, err)

			return
		}
	}
}

// callEchoRepeatedly keeps SDK-originated traffic in flight on the same
// connection the raw writes target.
func callEchoRepeatedly(t *testing.T, session *mcp.ClientSession, calls int) {
	t.Helper()

	for i := range calls {
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "echo",
			Arguments: map[string]any{"text": fmt.Sprintf("call-%d", i)},
		})
		if err != nil {
			t.Errorf("tool call %d: %v", i, err)

			return
		}

		if res.IsError {
			t.Errorf("tool call %d returned an error result", i)

			return
		}
	}
}

// drainNotifications receives want channel notifications from the tee,
// asserts each is framed as a notification with intact params, and returns
// the set of content markers seen.
func drainNotifications(t *testing.T, notifications <-chan *jsonrpc.Request, want int) map[string]bool {
	t.Helper()

	received := make(map[string]bool, want)

	for range want {
		select {
		case req := <-notifications:
			assert.False(t, req.IsCall(), "channel notification must carry no request ID")

			var params struct {
				Content string            `json:"content"`
				Meta    map[string]string `json:"meta"`
			}

			require.NoError(t, json.Unmarshal(req.Params, &params))
			assert.Equal(t, map[string]string{"change": "42"}, params.Meta)

			received[params.Content] = true

		case <-time.After(10 * time.Second):
			t.Fatalf("received %d of %d notifications before timeout", len(received), want)
		}
	}

	return received
}

func registerEchoTool(srv *mcp.Server) {
	type echoArgs struct {
		Text string `json:"text"`
	}

	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echo the input back"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: args.Text}},
			}, nil, nil
		})
}

// captureTransport keeps a reference to the connection produced by the inner
// transport and hands the connection to the SDK unwrapped. This is the seam
// the production channel emitter uses for raw notification writes.
type captureTransport struct {
	inner mcp.Transport

	conn mcp.Connection `exhaustruct:"optional"`
}

func (t *captureTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, e.NewFrom("connect inner transport", err)
	}

	t.conn = conn

	return conn, nil
}

// teeTransport wraps the client side of the pair and copies every channel
// notification the client reads into a buffered channel, exposing raw frames
// an SDK client would otherwise drop (the method is not in its dispatch
// table).
type teeTransport struct {
	inner         mcp.Transport
	notifications chan *jsonrpc.Request
}

func (t *teeTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, e.NewFrom("connect inner transport", err)
	}

	return &teeConn{Connection: conn, notifications: t.notifications}, nil
}

type teeConn struct {
	mcp.Connection

	notifications chan *jsonrpc.Request
}

func (c *teeConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	if err != nil {
		// Read errors pass through unwrapped: the SDK detects io.EOF on them.
		return msg, err //nolint:wrapcheck
	}

	if req, ok := msg.(*jsonrpc.Request); ok && req.Method == channelMethod {
		c.notifications <- req
	}

	return msg, nil
}
