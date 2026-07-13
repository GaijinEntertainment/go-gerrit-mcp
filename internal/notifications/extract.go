package notifications

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

// Transition is a status movement observed between ticks.
type Transition struct {
	From string
	To   string
}

// IsTerminal reports whether a change status ends its subscription: a merged
// or abandoned change almost never needs further watching, and the model
// learns the subscription ended from the final notification rather than
// inferring it from silence (ADR 2.2).
func IsTerminal(status string) bool {
	return status == "MERGED" || status == "ABANDONED"
}

// Delta is one change's new review activity since the previous report,
// together with the context a renderer needs to compose a self-sufficient
// payload.
type Delta struct {
	// Change is the detailed fetch backing the delta.
	Change *gerrit.ChangeInfo
	// Messages are the change messages dated after the cursor, in fetch
	// order.
	Messages []gerrit.ChangeMessageInfo
	// Votes are the vote actions dated after the cursor, ordered by label
	// then fetch order.
	Votes []Vote
	// Comments is the change's full published comment listing by file path —
	// thread assembly needs the surrounding thread, not only what is new.
	Comments map[string][]gerrit.CommentInfo
	// NewComments marks the comment IDs that are new since the cursor.
	NewComments map[string]bool
	// Transition is the status movement, nil when the status held.
	Transition *Transition
}

// Empty reports whether the delta carries nothing to notify about.
func (d *Delta) Empty() bool {
	return len(d.Messages) == 0 && len(d.Votes) == 0 && len(d.NewComments) == 0 && d.Transition == nil
}

// extractDelta computes what is new in the detailed fetch and the comment
// listing relative to the cursor, and returns the cursor acknowledging it.
// Pure: no I/O, no store access — committing the cursor is the caller's
// decision.
func extractDelta(cur Cursor, info *gerrit.ChangeInfo, comments map[string][]gerrit.CommentInfo) (*Delta, Cursor) {
	next := cur

	next.Updated = info.Updated.Time

	d := &Delta{
		Change:      info,
		Messages:    nil,
		Votes:       nil,
		Comments:    comments,
		NewComments: nil,
		Transition:  nil,
	}

	d.Messages, next.Messages = newMessages(cur.Messages, info.Messages)
	d.Votes, next.Votes = newVotes(cur.Votes, info.Labels)
	d.NewComments, next.Comments = newComments(cur.Comments, comments)

	if info.Status != cur.Status {
		d.Transition = &Transition{From: cur.Status, To: info.Status}
		next.Status = info.Status
	}

	return d, next
}

// newMessages selects the change messages dated after the mark and reports
// the advanced mark. Selection compares against the incoming mark only, so
// out-of-order same-tick messages cannot shadow each other.
func newMessages(mark time.Time, messages []gerrit.ChangeMessageInfo) ([]gerrit.ChangeMessageInfo, time.Time) {
	var fresh []gerrit.ChangeMessageInfo

	for _, msg := range messages {
		if !msg.Date.After(mark) {
			continue
		}

		fresh = append(fresh, msg)
	}

	for _, msg := range fresh {
		mark = laterOf(mark, msg.Date.Time)
	}

	return fresh, mark
}

// newVotes selects the vote actions dated after the mark, ordered by label
// name, and reports the advanced mark. A reviewer entry without a parseable
// date never voted.
func newVotes(mark time.Time, labels map[string]gerrit.LabelInfo) ([]Vote, time.Time) {
	var fresh []Vote

	for _, label := range slices.Sorted(maps.Keys(labels)) {
		for _, approval := range labels[label].All {
			date, err := time.Parse(gerritTimeLayout, approval.Date)
			if err != nil || !date.After(mark) {
				continue
			}

			fresh = append(fresh, Vote{Label: label, Value: approval.Value, Voter: approval.AccountInfo, Date: date})
		}
	}

	for _, v := range fresh {
		mark = laterOf(mark, v.Date)
	}

	return fresh, mark
}

// newComments marks the comment IDs updated after the mark and reports the
// advanced mark.
func newComments(mark time.Time, comments map[string][]gerrit.CommentInfo) (map[string]bool, time.Time) {
	fresh := make(map[string]bool)

	for _, list := range comments {
		for _, ci := range list {
			if ci.Updated == nil || !ci.Updated.After(mark) {
				continue
			}

			fresh[ci.ID] = true
		}
	}

	for _, list := range comments {
		for _, ci := range list {
			if fresh[ci.ID] {
				mark = laterOf(mark, ci.Updated.Time)
			}
		}
	}

	return fresh, mark
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}
