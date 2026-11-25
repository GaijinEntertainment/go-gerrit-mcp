package activity

import (
	"maps"
	"slices"
	"time"

	gerrit "github.com/andygrunwald/go-gerrit"
)

// gerritTimeLayout is Gerrit's timestamp format. ApprovalInfo dates arrive
// as plain strings in it rather than as typed timestamps.
const gerritTimeLayout = "2006-01-02 15:04:05.000000000"

// Vote is one dated vote action on a label. A zero value with a fresh date
// is a vote removal — still an action worth reporting.
type Vote struct {
	Label string
	Value int
	Voter gerrit.AccountInfo
	Date  time.Time
}

// Transition is a status movement between two reports.
type Transition struct {
	From string
	To   string
}

// IsTerminal reports whether a change status ends the reporting on the
// change: a merged or abandoned change almost never needs further watching
// (ADR 2.2).
func IsTerminal(status string) bool {
	return status == "MERGED" || status == "ABANDONED"
}

// Delta is one change's new review activity since the previous report,
// together with the context a renderer needs to compose a self-sufficient
// payload.
type Delta struct {
	// Change is the detailed fetch backing the delta.
	Change *gerrit.ChangeInfo
	// Messages are the change messages dated after the previous report, in
	// fetch order.
	Messages []gerrit.ChangeMessageInfo
	// Votes are the vote actions dated after the previous report, ordered by
	// label then fetch order.
	Votes []Vote
	// Comments is the change's full published comment listing by file path —
	// thread assembly needs the surrounding thread, not only what is new.
	Comments map[string][]gerrit.CommentInfo
	// NewComments marks the comment IDs that are new since the previous
	// report.
	NewComments map[string]bool
	// Transition is the status movement, nil when the status held.
	Transition *Transition
}

// Empty reports whether the delta carries no activity to report.
func (d *Delta) Empty() bool {
	return len(d.Messages) == 0 && len(d.Votes) == 0 && len(d.NewComments) == 0 && d.Transition == nil
}

// extractDelta computes what is new in the detailed fetch and the comment
// listing past since and status, the point the previous report reached.
// Pure: no I/O.
func extractDelta(
	since time.Time, status string, info *gerrit.ChangeInfo, comments map[string][]gerrit.CommentInfo,
) *Delta {
	d := &Delta{
		Change:      info,
		Messages:    newMessages(since, info.Messages),
		Votes:       newVotes(since, info.Labels),
		Comments:    comments,
		NewComments: newComments(since, info.Updated.Time, comments),
		Transition:  nil,
	}

	if info.Status != status {
		d.Transition = &Transition{From: status, To: info.Status}
	}

	return d
}

// newMessages selects the change messages dated after since, in fetch order.
func newMessages(since time.Time, messages []gerrit.ChangeMessageInfo) []gerrit.ChangeMessageInfo {
	var fresh []gerrit.ChangeMessageInfo

	for _, msg := range messages {
		if msg.Date.After(since) {
			fresh = append(fresh, msg)
		}
	}

	return fresh
}

// newVotes selects the vote actions dated after since, ordered by label
// name. A reviewer entry without a parseable date never voted.
func newVotes(since time.Time, labels map[string]gerrit.LabelInfo) []Vote {
	var fresh []Vote

	for _, label := range slices.Sorted(maps.Keys(labels)) {
		for _, approval := range labels[label].All {
			date, err := time.Parse(gerritTimeLayout, approval.Date)
			if err != nil || !date.After(since) {
				continue
			}

			fresh = append(fresh, Vote{Label: label, Value: approval.Value, Voter: approval.AccountInfo, Date: date})
		}
	}

	return fresh
}

// newComments marks the IDs of the comments updated after since and not
// after until, the point the report returns. The comment listing is a later
// read than the change, so a comment past that point waits for the report
// whose point covers it.
func newComments(since, until time.Time, comments map[string][]gerrit.CommentInfo) map[string]bool {
	fresh := make(map[string]bool)

	for _, list := range comments {
		for _, ci := range list {
			if ci.Updated != nil && ci.Updated.After(since) && !ci.Updated.After(until) {
				fresh[ci.ID] = true
			}
		}
	}

	return fresh
}
