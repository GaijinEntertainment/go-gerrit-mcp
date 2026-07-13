package notifications

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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

// Renderer composes channel payloads: llmxml content plus the routing meta.
// It lives behind an interface because payloads reuse the rendering
// vocabulary of the tools package, which sits above this one.
type Renderer interface {
	// Render composes a delta payload.
	Render(d *Delta) (content string, meta map[string]string)
	// RenderEnded composes the notice ending a subscription for a reason
	// other than a terminal status — a change that became inaccessible.
	RenderEnded(change int, reason string) (content string, meta map[string]string)
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

	seen := make(map[int]bool, len(res.Changes))

	for i := range res.Changes {
		if ctx.Err() != nil {
			return
		}

		ci := &res.Changes[i]

		seen[ci.Number] = true

		cur, ok := p.store.Cursor(ci.Number)
		if !ok || !ci.Updated.After(cur.Updated) {
			continue
		}

		p.process(ctx, ci.Number, cur)
	}

	// A subscribed change the batched query no longer returns was deleted or
	// hidden from this account — its own change: clause matches nothing.
	for _, change := range changes {
		if seen[change] || ctx.Err() != nil {
			continue
		}

		p.confirmAccess(ctx, change)
	}
}

// confirmAccess double-checks a change missing from the query result with a
// direct fetch: query staleness must not end a subscription, so only a
// classified access failure does.
func (p *Poller) confirmAccess(ctx context.Context, change int) {
	_, err := p.client.GetChange(ctx, strconv.Itoa(change))
	if err == nil {
		return
	}

	reason, gone := inaccessibleReason(err)
	if !gone {
		p.lgr.Error("review notifications access check", "change", change, "error", err)

		return
	}

	p.endInaccessible(ctx, change, reason)
}

// endInaccessible ends a subscription whose change this session can no
// longer read, announcing the reason; silence would read as a quiet change,
// not a lost one.
func (p *Poller) endInaccessible(ctx context.Context, change int, reason string) {
	if !p.store.Remove(change) {
		return
	}

	content, meta := p.renderer.RenderEnded(change, reason)

	if err := p.emitter.Emit(ctx, content, meta); err != nil {
		p.lgr.Error("review notification emit", "change", change, "error", err)
	}
}

// inaccessibleReason classifies an error as one that ends a subscription:
// the change is gone for this session rather than momentarily unreachable.
func inaccessibleReason(err error) (string, bool) {
	if errors.Is(err, gerritclient.ErrProjectScope) {
		return "the change is outside the configured project scope", true
	}

	switch gerritclient.APIStatus(err) {
	case http.StatusNotFound, http.StatusForbidden:
		return "the change was deleted or is no longer visible to this account", true
	default:
		return "", false
	}
}

// process fetches a moved change in detail, extracts the activity delta
// against the cursor, commits the cursor, and emits the delta. A fetch
// failure leaves the cursor untouched so the next tick retries the change.
func (p *Poller) process(ctx context.Context, change int, cur Cursor) {
	id := strconv.Itoa(change)

	info, err := p.client.GetChange(ctx, id)
	if err != nil {
		p.fetchFailed(ctx, change, "detail fetch", err)

		return
	}

	comments, err := p.client.ListChangeComments(ctx, id)
	if err != nil {
		p.fetchFailed(ctx, change, "comment fetch", err)

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

// fetchFailed routes a mid-process fetch error: a classified access loss
// ends the subscription with a notice, anything else is logged and the
// untouched cursor retries the change next tick.
func (p *Poller) fetchFailed(ctx context.Context, change int, what string, err error) {
	if reason, gone := inaccessibleReason(err); gone {
		p.endInaccessible(ctx, change, reason)

		return
	}

	p.lgr.Error("review notifications "+what, "change", change, "error", err)
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
