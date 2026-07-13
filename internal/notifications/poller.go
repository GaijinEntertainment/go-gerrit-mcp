package notifications

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

// Emitter delivers one rendered notification into the agent's session.
// Content is a rendered llmxml payload; meta carries routing context that
// becomes tag attributes on the injected block, so keys must be limited to
// letters, digits, and underscores.
type Emitter interface {
	Emit(ctx context.Context, content string, meta map[string]string) error
}

// Renderer composes a delta into the channel payload: llmxml content plus
// the routing meta. It lives behind an interface because the payload reuses
// the rendering vocabulary of the tools package, which sits above this one.
type Renderer interface {
	Render(d *Delta) (content string, meta map[string]string)
}

// Poller periodically queries Gerrit for movement on subscribed changes and
// hands detected activity to the emitter. Failures are logged and retried on
// the next tick — a background loop has no caller to return errors to, and a
// transient Gerrit failure must not end the session's subscriptions.
type Poller struct {
	store    *Store
	client   *gerritclient.Client
	renderer Renderer
	emitter  Emitter
	filters  Filters
	interval time.Duration
	lgr      *slog.Logger
}

// PollerConfig carries everything a poller is assembled from.
type PollerConfig struct {
	Store    *Store
	Client   *gerritclient.Client
	Renderer Renderer
	Emitter  Emitter
	Filters  Filters
	Interval time.Duration
	Logger   *slog.Logger
}

// NewPoller assembles a poller over the given subscription store. It does not
// start anything; the caller runs [Poller.Run] on its own goroutine.
func NewPoller(cfg PollerConfig) *Poller {
	return &Poller{
		store:    cfg.Store,
		client:   cfg.Client,
		renderer: cfg.Renderer,
		emitter:  cfg.Emitter,
		filters:  cfg.Filters,
		interval: cfg.Interval,
		lgr:      cfg.Logger,
	}
}

// Run polls at the configured interval until ctx is cancelled. It blocks;
// the caller owns the goroutine.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// tick runs one poll cycle: a single batched query over the subscription
// snapshot, then a detail pass over every change whose updated timestamp
// moved past its cursor. An empty snapshot skips the cycle without touching
// the network.
func (p *Poller) tick(ctx context.Context) {
	changes := p.store.Changes()
	if len(changes) == 0 {
		return
	}

	res, err := p.client.QueryChanges(ctx, batchQuery(changes), len(changes), 0)
	if err != nil {
		p.lgr.Error("review notifications poll", "error", err)

		return
	}

	for i := range res.Changes {
		ci := &res.Changes[i]

		cur, ok := p.store.Cursor(ci.Number)
		if !ok || !ci.Updated.After(cur.Updated) {
			continue
		}

		p.process(ctx, ci.Number, cur)
	}
}

// process fetches a moved change in detail, extracts the activity delta
// against the cursor, commits the cursor, and emits the delta. A fetch
// failure leaves the cursor untouched so the next tick retries the change.
func (p *Poller) process(ctx context.Context, change int, cur Cursor) {
	id := strconv.Itoa(change)

	info, err := p.client.GetChange(ctx, id)
	if err != nil {
		p.lgr.Error("review notifications detail fetch", "change", change, "error", err)

		return
	}

	comments, err := p.client.ListChangeComments(ctx, id)
	if err != nil {
		p.lgr.Error("review notifications comment fetch", "change", change, "error", err)

		return
	}

	delta, next := extractDelta(cur, info, comments)

	// Filters run between extraction and rendering, so excluded activity
	// never reaches the model — including inside a final payload.
	p.filters.apply(delta)

	// A terminal status ends the subscription: the change leaves the store
	// first — an unsubscribe racing this tick wins — and the final
	// notification carries the transition together with any same-tick
	// activity. Terminal implies a transition, so the delta is never empty.
	if IsTerminal(info.Status) {
		if !p.store.Remove(change) {
			return
		}

		p.emit(ctx, delta)

		return
	}

	// An unsubscribe racing this tick wins: nothing is committed or emitted.
	if !p.store.SetCursor(change, next) {
		return
	}

	if delta.Empty() {
		return
	}

	p.emit(ctx, delta)
}

func (p *Poller) emit(ctx context.Context, d *Delta) {
	content, meta := p.renderer.Render(d)

	if err := p.emitter.Emit(ctx, content, meta); err != nil {
		p.lgr.Error("review notification emit", "change", d.Change.Number, "error", err)
	}
}

// batchQuery composes one change:A OR change:B query over the snapshot, so a
// tick costs a single request regardless of subscription count.
func batchQuery(changes []int) string {
	clauses := make([]string, len(changes))
	for i, n := range changes {
		clauses[i] = "change:" + strconv.Itoa(n)
	}

	return strings.Join(clauses, " OR ")
}
