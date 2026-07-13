package notifications

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	gerrit "github.com/andygrunwald/go-gerrit"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

// Emitter delivers one rendered notification into the agent's session.
// Content is a rendered llmxml payload; meta carries routing context that
// becomes tag attributes on the injected block, so keys must be limited to
// letters, digits, and underscores.
type Emitter interface {
	Emit(ctx context.Context, content string, meta map[string]string) error
}

// Poller periodically queries Gerrit for movement on subscribed changes and
// hands detected activity to the emitter. Failures are logged and retried on
// the next tick — a background loop has no caller to return errors to, and a
// transient Gerrit failure must not end the session's subscriptions.
type Poller struct {
	store    *Store
	client   *gerritclient.Client
	emitter  Emitter
	interval time.Duration
	lgr      *slog.Logger
}

// NewPoller assembles a poller over the given subscription store. It does not
// start anything; the caller runs [Poller.Run] on its own goroutine.
func NewPoller(
	store *Store, client *gerritclient.Client, emitter Emitter, interval time.Duration, lgr *slog.Logger,
) *Poller {
	return &Poller{
		store:    store,
		client:   client,
		emitter:  emitter,
		interval: interval,
		lgr:      lgr,
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
	meta := map[string]string{"change": strconv.Itoa(d.Change.Number)}

	if err := p.emitter.Emit(ctx, renderActivity(d.Change), meta); err != nil {
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

// renderActivity renders the tracer-bullet payload: the fact of movement on a
// subscribed change. Activity details (messages, votes, comment threads)
// arrive with the deltas phase.
func renderActivity(ci *gerrit.ChangeInfo) string {
	return llmxml.NewElement("review_activity",
		llmxml.Attr("change", ci.Number),
		llmxml.Attr("project", ci.Project),
		llmxml.Attr("status", ci.Status),
		llmxml.Attr("updated", ci.Updated.UTC().Format(time.RFC3339)),
	).String()
}
