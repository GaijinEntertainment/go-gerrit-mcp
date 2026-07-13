package notifications

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.gaijin.team/go/golib/e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

const selfJSON = ")]}'\n" + `{"_account_id":42,"name":"Review Bot","username":"bot"}`

// queryJSON renders the batched-query result for change 123; updated is a
// Gerrit-format timestamp such as "2026-07-01 10:00:00.000000000".
func queryJSON(updated string) string {
	return ")]}'\n" + `[{"_number":123,"project":"core","status":"NEW","updated":"` + updated + `"}]`
}

// detailJSON renders the detailed fetch of change 123 carrying one change
// message dated at updated.
func detailJSON(updated string) string {
	return ")]}'\n" + `{"_number":123,"project":"core","status":"NEW","updated":"` + updated + `",` +
		`"messages":[{"id":"m1","author":{"_account_id":8,"username":"bob"},"date":"` + updated + `",` +
		`"message":"ping","_revision_number":2}]}`
}

// quietDetailJSON renders a detailed fetch whose updated moved with nothing
// extractable behind it.
func quietDetailJSON(updated string) string {
	return ")]}'\n" + `{"_number":123,"project":"core","status":"NEW","updated":"` + updated + `"}`
}

type emitCall struct {
	content string
	meta    map[string]string
}

// fakeEmitter records emissions; a non-nil err makes every Emit fail.
type fakeEmitter struct {
	mu    sync.Mutex `exhaustruct:"optional"`
	calls []emitCall `exhaustruct:"optional"`
	err   error      `exhaustruct:"optional"`
}

func (f *fakeEmitter) Emit(_ context.Context, content string, meta map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, emitCall{content: content, meta: meta})

	return f.err
}

func (f *fakeEmitter) recorded() []emitCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]emitCall(nil), f.calls...)
}

// gerritStub serves the four endpoints the poller path touches, counting
// query and detail requests. Response bodies are swappable per test; an
// empty body answers 500.
type gerritStub struct {
	queries atomic.Int64 `exhaustruct:"optional"`
	details atomic.Int64 `exhaustruct:"optional"`

	mu       sync.Mutex `exhaustruct:"optional"`
	query    string     `exhaustruct:"optional"`
	detail   string     `exhaustruct:"optional"`
	comments string     `exhaustruct:"optional"`
}

func (g *gerritStub) set(query, detail string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.query = query
	g.detail = detail
}

func (g *gerritStub) setComments(comments string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.comments = comments
}

func respond(w http.ResponseWriter, body string) {
	if body == "" {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	_, _ = w.Write([]byte(body))
}

func (g *gerritStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()

	query, detail, comments := g.query, g.detail, g.comments

	g.mu.Unlock()

	switch r.URL.Path {
	case "/a/accounts/self":
		_, _ = w.Write([]byte(selfJSON))

	case "/a/changes/":
		g.queries.Add(1)
		respond(w, query)

	case "/a/changes/123":
		g.details.Add(1)
		respond(w, detail)

	case "/a/changes/123/comments":
		respond(w, comments)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// pollerFixture bundles a poller with the stub Gerrit, the recording
// emitter, and the captured log output behind it.
type pollerFixture struct {
	poller  *Poller
	stub    *gerritStub
	emitter *fakeEmitter
	logs    *bytes.Buffer
}

// newTestPoller wires a poller against a stub Gerrit; the interval only
// matters for Run-based tests.
func newTestPoller(t *testing.T, interval time.Duration) *pollerFixture {
	t.Helper()

	stub := &gerritStub{}
	stub.set(")]}'\n[]", "")
	stub.setComments(")]}'\n{}")

	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	client, err := gerritclient.New(t.Context(), &config.Config{
		GerritURL:                          srv.URL,
		Username:                           "bot",
		Token:                              "s3cret",
		Groups:                             []config.Group{config.GroupRead},
		IncludeTools:                       nil,
		ExcludeTools:                       nil,
		Projects:                           nil,
		AllowForeignChanges:                false,
		ReviewNotifications:                true,
		ReviewNotificationsPollInterval:    interval,
		ReviewNotificationsIncludeOwn:      false,
		ReviewNotificationsExcludeAccounts: nil,
		ReviewNotificationsExcludePatterns: nil,
	})
	require.NoError(t, err)

	emitter := &fakeEmitter{}
	logs := &bytes.Buffer{}
	lgr := slog.New(slog.NewTextHandler(logs, nil))

	return &pollerFixture{
		poller:  NewPoller(NewStore(), client, emitter, interval, lgr),
		stub:    stub,
		emitter: emitter,
		logs:    logs,
	}
}

func seedCursor(t *testing.T) Cursor {
	t.Helper()

	return NewCursor(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), "NEW")
}

func Test_Poller_Tick(t *testing.T) {
	t.Parallel()

	const (
		movedAt = "2026-07-01 11:00:00.000000000"
		seedAt  = "2026-07-01 10:00:00.000000000"
	)

	t.Run("movement emits one delta and replay is silent", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), detailJSON(movedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())
		f.poller.tick(t.Context())

		calls := f.emitter.recorded()
		require.Len(t, calls, 1, "second tick over the same state must be silent")
		assert.Equal(t,
			`<review_activity change="123" project="core" status="NEW" updated="2026-07-01T11:00:00Z"/>`,
			calls[0].content)
		assert.Equal(t, map[string]string{"change": "123"}, calls[0].meta)

		assert.EqualValues(t, 1, f.stub.details.Load(), "replay must not re-fetch an unmoved change")
	})

	t.Run("movement with an empty delta commits silently", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), quietDetailJSON(movedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())
		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.EqualValues(t, 1, f.stub.details.Load(), "the committed cursor must stop repeat detail fetches")
	})

	t.Run("no movement fetches no detail and emits nothing", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(seedAt), detailJSON(seedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Zero(t, f.stub.details.Load())
	})

	t.Run("empty subscription set skips the query", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)

		f.poller.tick(t.Context())

		assert.Zero(t, f.stub.queries.Load(), "no subscriptions must mean no network request")
		assert.Empty(t, f.emitter.recorded())
	})

	t.Run("query failure is logged and the next tick recovers", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set("", detailJSON(movedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Contains(t, f.logs.String(), "review notifications poll")

		f.stub.set(queryJSON(movedAt), detailJSON(movedAt))

		f.poller.tick(t.Context())

		assert.Len(t, f.emitter.recorded(), 1, "poll failure must not end the subscription")
	})

	t.Run("detail failure leaves the cursor for a retry", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), "")

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Contains(t, f.logs.String(), "review notifications detail fetch")

		f.stub.set(queryJSON(movedAt), detailJSON(movedAt))

		f.poller.tick(t.Context())

		assert.Len(t, f.emitter.recorded(), 1, "the un-advanced cursor must retry the change")
	})

	t.Run("emit failure is logged and does not stop the tick", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), detailJSON(movedAt))

		f.emitter.err = e.New("session gone")

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Len(t, f.emitter.recorded(), 1)
		assert.Contains(t, f.logs.String(), "review notification emit")
	})

	t.Run("result for an unsubscribed change is ignored", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), detailJSON(movedAt))

		f.poller.store.Add(456, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Zero(t, f.stub.details.Load())
	})
}

func Test_Poller_Run(t *testing.T) {
	t.Parallel()

	const pollDeadline = 10 * time.Second

	f := newTestPoller(t, 5*time.Millisecond)
	f.stub.set(queryJSON("2026-07-01 11:00:00.000000000"), detailJSON("2026-07-01 11:00:00.000000000"))

	f.poller.store.Add(123, seedCursor(t))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		f.poller.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		return len(f.emitter.recorded()) > 0
	}, pollDeadline, time.Millisecond, "ticker must fire and emit")

	cancel()

	select {
	case <-done:
	case <-time.After(pollDeadline):
		t.Fatal("Run did not stop on context cancellation")
	}
}
