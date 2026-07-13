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

// changeJSON renders one change query result; updated is a Gerrit-format
// timestamp such as "2026-07-01 10:00:00.000000000".
func changeJSON(updated string) string {
	return ")]}'\n" + `[{"_number":123,"project":"core","status":"NEW","updated":"` + updated + `"}]`
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

// gerritStub serves the credential-validation endpoint plus change queries,
// counting query requests and answering them via the swappable handler.
type gerritStub struct {
	queries atomic.Int64                     `exhaustruct:"optional"`
	handler atomic.Pointer[http.HandlerFunc] `exhaustruct:"optional"`
}

func (g *gerritStub) setHandler(h http.HandlerFunc) {
	g.handler.Store(&h)
}

func (g *gerritStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/a/accounts/self" {
		_, _ = w.Write([]byte(selfJSON))

		return
	}

	g.queries.Add(1)
	(*g.handler.Load())(w, r)
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
	stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(")]}'\n[]"))
	})

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

func Test_Poller_Tick(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Run("movement emits once and advances the cursor", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(changeJSON("2026-07-01 11:00:00.000000000")))
		})

		f.poller.store.Add(123, base)

		f.poller.tick(t.Context())
		f.poller.tick(t.Context())

		calls := f.emitter.recorded()
		require.Len(t, calls, 1, "second tick over the same state must be silent")
		assert.Equal(t,
			`<review_activity change="123" project="core" status="NEW" updated="2026-07-01T11:00:00Z"/>`,
			calls[0].content)
		assert.Equal(t, map[string]string{"change": "123"}, calls[0].meta)
	})

	t.Run("no movement emits nothing", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(changeJSON("2026-07-01 10:00:00.000000000")))
		})

		f.poller.store.Add(123, base)

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
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
		f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		f.poller.store.Add(123, base)

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
		assert.Contains(t, f.logs.String(), "review notifications poll")

		f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(changeJSON("2026-07-01 11:00:00.000000000")))
		})

		f.poller.tick(t.Context())

		assert.Len(t, f.emitter.recorded(), 1, "poll failure must not end the subscription")
	})

	t.Run("emit failure is logged and does not stop the tick", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(changeJSON("2026-07-01 11:00:00.000000000")))
		})

		f.emitter.err = e.New("session gone")

		f.poller.store.Add(123, base)

		f.poller.tick(t.Context())

		assert.Len(t, f.emitter.recorded(), 1)
		assert.Contains(t, f.logs.String(), "review notification emit")
	})

	t.Run("result for an unsubscribed change is ignored", func(t *testing.T) {
		t.Parallel()

		f := newTestPoller(t, time.Minute)
		f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(changeJSON("2026-07-01 11:00:00.000000000")))
		})

		f.poller.store.Add(456, base)

		f.poller.tick(t.Context())

		assert.Empty(t, f.emitter.recorded())
	})
}

func Test_Poller_Run(t *testing.T) {
	t.Parallel()

	const pollDeadline = 10 * time.Second

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	f := newTestPoller(t, 5*time.Millisecond)
	f.stub.setHandler(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(changeJSON("2026-07-01 11:00:00.000000000")))
	})

	f.poller.store.Add(123, base)

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
