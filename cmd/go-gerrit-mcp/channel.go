package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

const (
	channelMethod            = "notifications/claude/channel"
	channelCapability        = "claude/channel"
	reviewActivityMethod     = "notifications/gerrit/review_activity"
	reviewActivityCapability = "gerrit/review_activity"
)

// constraint: Claude Code accepts only letters, digits, and underscores in channel meta keys.
var metaKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// constraint: The SDK requires the inner connection for session-state updates.
type captureTransport struct {
	inner mcp.Transport

	mu   sync.Mutex     `exhaustruct:"optional"`
	conn mcp.Connection `exhaustruct:"optional"`
}

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

func (t *captureTransport) connection() mcp.Connection {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.conn
}

type reviewParams struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// constraint: The pinned MCP SDK has no generic notification sender.
type reviewEmitter struct {
	transport *captureTransport
	lgr       *slog.Logger
}

var _ notifications.Emitter = (*reviewEmitter)(nil)

func (emitter *reviewEmitter) Emit(ctx context.Context, content string, meta map[string]string) error {
	conn := emitter.transport.connection()
	if conn == nil {
		emitter.lgr.Warn("review notification dropped: session not connected yet")

		return nil
	}

	raw, err := json.Marshal(reviewParams{Content: content, Meta: emitter.sanitizeMeta(meta)})
	if err != nil {
		return e.NewFrom("marshal review notification", err)
	}

	var errs []error

	for _, method := range []string{channelMethod, reviewActivityMethod} {
		req := &jsonrpc.Request{ID: jsonrpc.ID{}, Method: method, Params: raw, Extra: nil}
		if err := conn.Write(ctx, req); err != nil {
			errs = append(errs, e.NewFrom("write "+method, err))
		}
	}

	return errors.Join(errs...)
}

func (emitter *reviewEmitter) sanitizeMeta(meta map[string]string) map[string]string {
	clean := make(map[string]string, len(meta))

	for k, v := range meta {
		if !metaKeyPattern.MatchString(k) {
			emitter.lgr.Warn("review meta key dropped: Claude Code accepts letters, digits, and underscores only",
				"key", k)

			continue
		}

		clean[k] = v
	}

	return clean
}
