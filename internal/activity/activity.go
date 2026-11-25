// Package activity computes the review activity of Gerrit changes: what is
// new on a change past a point that the caller keeps (see docs/glossary.md:
// Review activity).
package activity

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	gerrit "github.com/andygrunwald/go-gerrit"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

// Request asks for one change's review activity past the point a
// previous report reached. The caller keeps that point; a zero Since asks
// for the starting point and reports no activity.
type Request struct {
	Change int
	// Since is the Updated value of the previous report for this change.
	Since time.Time
	// Status is the Status value of the previous report for this change.
	Status string
}

// Report is the outcome for one requested change.
type Report struct {
	Change int
	// Updated and Status are the Since and Status of the next request. They
	// repeat the request's values when the change was not read.
	Updated time.Time
	Status  string
	// Delta is the new activity that passed the filters, nil when there is
	// none.
	Delta *Delta
	// Inaccessible is the reason the account can no longer read the change,
	// empty while it can.
	Inaccessible string
	// Err is a fetch failure that left the change unread.
	Err error
}

// Collect reports the review activity of every requested change past
// its Since. It keeps no state: one batched query covers all requests, and
// a detail fetch goes only to a change whose updated timestamp moved past its
// Since or that the query did not list.
func Collect(
	ctx context.Context, client *gerritclient.Client, filters Filters, reqs []Request,
) ([]Report, error) {
	reports := make([]Report, len(reqs))
	if len(reqs) == 0 {
		return reports, nil
	}

	res, err := client.QueryChanges(ctx, batchQuery(reqs), len(reqs), 0)
	if err != nil {
		return nil, err
	}

	listed := make(map[int]*gerrit.ChangeInfo, len(res.Changes))
	for i := range res.Changes {
		listed[res.Changes[i].Number] = &res.Changes[i]
	}

	for i, req := range reqs {
		reports[i] = collectOne(ctx, client, filters, req, listed[req.Change])
	}

	return reports, nil
}

// collectOne reads one change. The batched query answers a request without
// Since and a change that did not move. A change that the query did not list
// still gets the detail fetch: query staleness must not read as lost access,
// so only a classified access failure does.
func collectOne(
	ctx context.Context, client *gerritclient.Client, filters Filters, req Request, listed *gerrit.ChangeInfo,
) Report {
	report := Report{
		Change:       req.Change,
		Updated:      req.Since,
		Status:       req.Status,
		Delta:        nil,
		Inaccessible: "",
		Err:          nil,
	}

	if listed != nil {
		if req.Since.IsZero() {
			report.Updated, report.Status = listed.Updated.Time, listed.Status

			return report
		}

		if !listed.Updated.After(req.Since) {
			return report
		}
	}

	id := strconv.Itoa(req.Change)

	info, err := client.GetChange(ctx, id)
	if err != nil {
		return report.unread(err)
	}

	if req.Since.IsZero() {
		report.Updated, report.Status = info.Updated.Time, info.Status

		return report
	}

	if !info.Updated.After(req.Since) {
		return report
	}

	comments, err := client.ListChangeComments(ctx, id)
	if err != nil {
		return report.unread(err)
	}

	delta := extractDelta(req.Since, req.Status, info, comments)

	filters.apply(delta)

	report.Updated, report.Status = info.Updated.Time, info.Status

	if !delta.Empty() {
		report.Delta = delta
	}

	return report
}

func (r Report) unread(err error) Report {
	if reason, isGone := inaccessibleReason(err); isGone {
		r.Inaccessible = reason

		return r
	}

	r.Err = err

	return r
}

// inaccessibleReason classifies an error as one that ends the reporting on a
// change: the change is gone for this account rather than momentarily
// unreachable.
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

// batchQuery composes one change:A OR change:B query, so a report costs a
// single request regardless of the number of changes.
func batchQuery(reqs []Request) string {
	clauses := make([]string, len(reqs))
	for i, req := range reqs {
		clauses[i] = "change:" + strconv.Itoa(req.Change)
	}

	return strings.Join(clauses, " OR ")
}
