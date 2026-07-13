package notifications

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// mergedDetailJSON renders a detailed fetch of change 123 that reached
// MERGED, carrying the submit message alongside the transition.
func mergedDetailJSON(updated string) string {
	return ")]}'\n" + `{"_number":123,"project":"core","status":"MERGED","updated":"` + updated + `",` +
		`"messages":[{"id":"m1","author":{"_account_id":8,"username":"bob"},"date":"` + updated + `",` +
		`"message":"Change has been merged","_revision_number":2}]}`
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

// fakeRenderer summarises the delta so tick tests can assert what reached
// the emitter without depending on the production payload vocabulary.
type fakeRenderer struct{}

func (fakeRenderer) Render(d *Delta) (string, map[string]string) {
	return fmt.Sprintf("delta change=%d messages=%d votes=%d comments=%d transition=%v",
			d.Change.Number, len(d.Messages), len(d.Votes), len(d.NewComments), d.Transition != nil),
		map[string]string{"change": strconv.Itoa(d.Change.Number)}
}

func (fakeRenderer) RenderEnded(change int, reason string) (string, map[string]string) {
	return fmt.Sprintf("ended change=%d reason=%q", change, reason),
		map[string]string{"change": strconv.Itoa(change)}
}

// gerritStub serves the four endpoints the poller path touches, counting
// query and detail requests. Response bodies are swappable per test; an
// empty body answers 500.
type gerritStub struct {
	queries atomic.Int64 `exhaustruct:"optional"`
	details atomic.Int64 `exhaustruct:"optional"`

	mu         sync.Mutex          `exhaustruct:"optional"`
	query      string              `exhaustruct:"optional"`
	detail     string              `exhaustruct:"optional"`
	detailCode int                 `exhaustruct:"optional"`
	detailHook func(*http.Request) `exhaustruct:"optional"`
	comments   string              `exhaustruct:"optional"`
}

func (g *gerritStub) set(query, detail string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.query = query
	g.detail = detail
	g.detailCode = 0
}

// setDetailCode makes the detail endpoint answer the given HTTP status with
// a plain-text body.
func (g *gerritStub) setDetailCode(code int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.detailCode = code
}

// setDetailHook installs a callback the detail endpoint runs before
// responding; used to block a fetch until its request context cancels.
func (g *gerritStub) setDetailHook(hook func(*http.Request)) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.detailHook = hook
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

	query, detail, detailCode, detailHook, comments := g.query, g.detail, g.detailCode, g.detailHook, g.comments

	g.mu.Unlock()

	switch r.URL.Path {
	case "/a/accounts/self":
		_, _ = w.Write([]byte(selfJSON))

	case "/a/changes/":
		g.queries.Add(1)
		respond(w, query)

	case "/a/changes/123":
		g.details.Add(1)

		if detailHook != nil {
			detailHook(r)
		}

		if detailCode != 0 {
			w.WriteHeader(detailCode)

			_, _ = w.Write([]byte("Not found: change 123"))

			return
		}

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
// matters for Run-based tests, and the optional filters default to none.
func newTestPoller(t *testing.T, interval time.Duration, filters ...Filters) *pollerFixture {
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

	var f Filters

	if len(filters) > 0 {
		f = filters[0]
	}

	return &pollerFixture{
		poller: NewPoller(PollerConfig{
			Store:    NewStore(),
			Client:   client,
			Renderer: fakeRenderer{},
			Emitter:  emitter,
			Filters:  f,
			Interval: interval,
			Logger:   lgr,
		}),
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
		assert.Equal(t, "delta change=123 messages=1 votes=0 comments=0 transition=false", calls[0].content)
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

	t.Run("fully filtered delta emits nothing but commits the cursor", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute, Filters{ExcludeAccounts: []string{"bob"}})
		f.stub.set(queryJSON(movedAt), detailJSON(movedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())
		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded(), "excluded activity must never reach the emitter")
		assert.EqualValues(t, 1, f.stub.details.Load(), "the filtered tick must still commit the cursor")
	})

	t.Run("terminal status ends the subscription after one final emission", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), mergedDetailJSON(movedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		calls := f.emitter.recorded()
		require.Len(t, calls, 1)
		assert.Equal(t, "delta change=123 messages=1 votes=0 comments=0 transition=true", calls[0].content,
			"same-tick activity must ride in the final notification")
		assert.Empty(t, f.poller.store.Changes(), "the change must leave the store")

		f.poller.tick(t.Context())

		assert.Len(t, f.emitter.recorded(), 1, "an ended subscription must stay silent")
		assert.EqualValues(t, 1, f.stub.queries.Load(), "an empty store must stop polling entirely")
	})

	t.Run("lost access mid-process ends the subscription with a notice", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(queryJSON(movedAt), "")
		f.stub.setDetailCode(http.StatusNotFound)

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		calls := f.emitter.recorded()
		require.Len(t, calls, 1)
		assert.Equal(t,
			`ended change=123 reason="the change was deleted or is no longer visible to this account"`,
			calls[0].content)
		assert.Empty(t, f.poller.store.Changes())
	})

	t.Run("change missing from the query is confirmed before its subscription ends", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(")]}'\n[]", "")
		f.stub.setDetailCode(http.StatusNotFound)

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		require.Len(t, f.emitter.recorded(), 1)
		assert.Empty(t, f.poller.store.Changes())
		assert.EqualValues(t, 1, f.stub.details.Load(), "the absence must be confirmed by a direct fetch")
	})

	t.Run("query staleness alone does not end a subscription", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.set(")]}'\n[]", quietDetailJSON(seedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Equal(t, []int{123}, f.poller.store.Changes(), "a fetchable change stays subscribed")
		assert.EqualValues(t, 1, f.stub.details.Load())
	})

	t.Run("result for an unsubscribed change is ignored", func(t *testing.T) {
		t.Parallel()

		foreignQuery := ")]}'\n" + `[{"_number":456,"project":"core","status":"NEW","updated":"` + movedAt + `"}]`

		f := newTestPoller(t, time.Minute)
		f.stub.set(foreignQuery, quietDetailJSON(seedAt))

		f.poller.store.Add(123, seedCursor(t))

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Equal(t, []int{123}, f.poller.store.Changes(),
			"the confirmed-fetchable subscription must survive the foreign result")
	})
}

func Test_InaccessibleReason(t *testing.T) {
	t.Parallel()

	t.Run("project scope loss is classified with its own reason", func(t *testing.T) {
		t.Parallel()

		reason, gone := inaccessibleReason(gerritclient.ErrProjectScope.WithField("change", "123"))
		assert.True(t, gone)
		assert.Contains(t, reason, "project scope")
	})

	t.Run("other failures are transient", func(t *testing.T) {
		t.Parallel()

		_, gone := inaccessibleReason(e.New("gerrit is down"))
		assert.False(t, gone)

		_, gone = inaccessibleReason(context.Canceled)
		assert.False(t, gone)
	})
}

// Test_Poller_MidTickCancellation proves a cancelled context aborts a tick
// promptly — even mid-fetch — without emitting and without ending the
// subscription.
func Test_Poller_MidTickCancellation(t *testing.T) {
	t.Parallel()

	const deadline = 10 * time.Second

	f := newTestPoller(t, time.Minute)

	started := make(chan struct{})
	f.stub.setDetailHook(func(r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	f.stub.set(queryJSON("2026-07-01 11:00:00.000000000"), detailJSON("2026-07-01 11:00:00.000000000"))

	f.poller.store.Add(123, seedCursor(t))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		f.poller.tick(ctx)
	}()

	select {
	case <-started:
	case <-time.After(deadline):
		t.Fatal("detail fetch never started")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatal("tick did not stop on context cancellation")
	}

	assert.Empty(t, f.emitter.recorded(), "a cancelled tick must not emit")
	assert.Equal(t, []int{123}, f.poller.store.Changes(), "cancellation must not end the subscription")
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
