package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"sync"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

// channelMethod is the notification method of the Claude Code channels
// contract (ADR 2.1, docs/glossary.md: Channel).
const channelMethod = "notifications/claude/channel"

// channelCapability is the experimental capability declared at initialize
// when review notifications are enabled.
const channelCapability = "claude/channel"

// metaKeyPattern matches meta keys the client accepts as tag attributes;
// anything else is silently dropped on the client side.
var metaKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// captureTransport hands the inner transport's connection to the SDK
// unwrapped and keeps a reference for raw notification writes. Unwrapped
// matters: the SDK type-asserts the connection to an unexported interface
// for session-state updates, and a proxying wrapper would silently disable
// that path (pinned by Test_Learning_RawNotificationWrite).
type captureTransport struct {
	inner mcp.Transport

	mu   sync.Mutex     `exhaustruct:"optional"`
	conn mcp.Connection `exhaustruct:"optional"`
}

// Connect implements [mcp.Transport].
func (t *captureTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, e.NewFrom("connect inner transport", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.conn = conn

	return conn, nil
}

// connection reports the captured connection, nil before the session
// connects.
func (t *captureTransport) connection() mcp.Connection {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.conn
}

// channelParams is the notifications/claude/channel payload: content becomes
// the body of the <channel> block injected into the session; each meta key
// becomes a tag attribute on it.
type channelParams struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// channelEmitter delivers notifications as ID-less JSON-RPC requests written
// straight to the captured connection — the pinned SDK exposes no generic
// notification sender (ADR 2.1). The seam is deliberately this narrow so an
// SDK-native sender can replace it without touching callers.
type channelEmitter struct {
	transport *captureTransport
	lgr       *slog.Logger
}

var _ notifications.Emitter = (*channelEmitter)(nil)

// Emit implements [notifications.Emitter]. Delivery is best-effort by
// contract: an emission racing server startup finds no connection and is
// dropped with a log — the client-side outcome of an undeliverable event is
// the same silence.
func (ce *channelEmitter) Emit(ctx context.Context, content string, meta map[string]string) error {
	conn := ce.transport.connection()
	if conn == nil {
		ce.lgr.Warn("channel notification dropped: session not connected yet")

		return nil
	}

	raw, err := json.Marshal(channelParams{Content: content, Meta: ce.sanitizeMeta(meta)})
	if err != nil {
		return e.NewFrom("marshal channel params", err)
	}

	// A zero ID is what makes this request a JSON-RPC notification.
	req := &jsonrpc.Request{ID: jsonrpc.ID{}, Method: channelMethod, Params: raw, Extra: nil}

	if err := conn.Write(ctx, req); err != nil {
		return e.NewFrom("write channel notification", err)
	}

	return nil
}

// sanitizeMeta drops meta keys the client would silently discard, naming
// each dropped key on stderr so the loss is visible on our side.
func (ce *channelEmitter) sanitizeMeta(meta map[string]string) map[string]string {
	clean := make(map[string]string, len(meta))

	for k, v := range meta {
		if !metaKeyPattern.MatchString(k) {
			ce.lgr.Warn("channel meta key dropped: the client accepts letters, digits, and underscores only",
				"key", k)

			continue
		}

		clean[k] = v
	}

	return clean
}
