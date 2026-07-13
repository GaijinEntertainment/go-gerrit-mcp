package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const receiveDeadline = 10 * time.Second

// observedTransport wraps the client side of an in-memory pair and copies
// every channel notification the client reads into a buffered channel; the
// SDK client itself drops the method as undispatchable.
type observedTransport struct {
	inner         mcp.Transport
	notifications chan *jsonrpc.Request
}

func (t *observedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, e.NewFrom("connect inner transport", err)
	}

	return &observedConn{Connection: conn, notifications: t.notifications}, nil
}

type observedConn struct {
	mcp.Connection

	notifications chan *jsonrpc.Request
}

func (c *observedConn) Read(ctx context.Context) (jsonrpc.Message, error) {
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

// channelFixture is a connected server/client pair with the production
// capture transport on the server side and an observing transport on the
// client side.
type channelFixture struct {
	emitter       *channelEmitter
	session       *mcp.ClientSession
	notifications chan *jsonrpc.Request
	logs          *bytes.Buffer
}

func newChannelFixture(t *testing.T) *channelFixture {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	capture := &captureTransport{inner: serverTransport}
	observed := &observedTransport{
		inner:         clientTransport,
		notifications: make(chan *jsonrpc.Request, 64),
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)

	type echoArgs struct {
		Text string `json:"text"`
	}

	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echo the input back"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: args.Text}},
			}, nil, nil
		})

	_, err := srv.Connect(t.Context(), capture, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	session, err := client.Connect(t.Context(), observed, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	logs := &bytes.Buffer{}

	return &channelFixture{
		emitter:       &channelEmitter{transport: capture, lgr: slog.New(slog.NewTextHandler(logs, nil))},
		session:       session,
		notifications: observed.notifications,
		logs:          logs,
	}
}

func (f *channelFixture) receive(t *testing.T) *jsonrpc.Request {
	t.Helper()

	select {
	case req := <-f.notifications:
		return req
	case <-time.After(receiveDeadline):
		t.Fatal("no channel notification before timeout")

		return nil
	}
}

func Test_ChannelEmitter_Emit(t *testing.T) {
	t.Parallel()

	t.Run("notification reaches the client with method and params intact", func(t *testing.T) {
		t.Parallel()

		f := newChannelFixture(t)

		content := `<review_activity change="123" status="NEW"/>`
		require.NoError(t, f.emitter.Emit(t.Context(), content, map[string]string{"change": "123"}))

		req := f.receive(t)

		assert.False(t, req.IsCall(), "channel notification must carry no request ID")
		assert.Equal(t, channelMethod, req.Method)

		var params channelParams

		require.NoError(t, json.Unmarshal(req.Params, &params))
		assert.Equal(t, content, params.Content)
		assert.Equal(t, map[string]string{"change": "123"}, params.Meta)
	})

	t.Run("emissions survive concurrent tool traffic", func(t *testing.T) {
		t.Parallel()

		const emissions = 20

		f := newChannelFixture(t)

		var wg sync.WaitGroup

		wg.Go(func() {
			for i := range emissions {
				content := fmt.Sprintf("event-%d", i)
				if err := f.emitter.Emit(t.Context(), content, map[string]string{"change": "42"}); err != nil {
					t.Errorf("emit %d: %v", i, err)

					return
				}
			}
		})

		wg.Go(func() {
			for i := range emissions {
				res, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{
					Name:      "echo",
					Arguments: map[string]any{"text": fmt.Sprintf("call-%d", i)},
				})
				if err != nil || res.IsError {
					t.Errorf("tool call %d failed: %v", i, err)

					return
				}
			}
		})

		wg.Wait()

		for range emissions {
			req := f.receive(t)
			assert.Equal(t, channelMethod, req.Method)
		}
	})

	t.Run("invalid meta keys dropped and named", func(t *testing.T) {
		t.Parallel()

		f := newChannelFixture(t)

		meta := map[string]string{"change": "123", "bad-key": "x", "worse key": "y"}
		require.NoError(t, f.emitter.Emit(t.Context(), "content", meta))

		var params channelParams

		require.NoError(t, json.Unmarshal(f.receive(t).Params, &params))

		assert.Equal(t, map[string]string{"change": "123"}, params.Meta)
		assert.Contains(t, f.logs.String(), "bad-key")
		assert.Contains(t, f.logs.String(), "worse key")
	})

	t.Run("emission before the session connects is dropped with a log", func(t *testing.T) {
		t.Parallel()

		logs := &bytes.Buffer{}
		emitter := &channelEmitter{
			transport: &captureTransport{inner: nil},
			lgr:       slog.New(slog.NewTextHandler(logs, nil)),
		}

		require.NoError(t, emitter.Emit(t.Context(), "content", nil))
		assert.Contains(t, logs.String(), "session not connected")
	})
}
