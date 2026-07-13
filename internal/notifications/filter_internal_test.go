package notifications

import (
	"regexp"
	"testing"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filterDelta builds a delta carrying one event of each kind: a message and
// a comment by bob, a vote by ci, and a transition.
func filterDelta(t *testing.T) *Delta {
	t.Helper()

	updated := ts(t, "2026-07-01 11:00:00.000000000")

	return &Delta{
		Change: detailedChange(t, "NEW"),
		Messages: []gerrit.ChangeMessageInfo{
			{
				ID: "m1", Author: account(8, "bob"), Date: updated,
				Message: "Build started", Tag: "", RevisionNumber: 2,
			},
		},
		Votes: []Vote{
			{Label: "Verified", Value: 1, Voter: account(9, "ci"), Date: updated.Time},
		},
		Comments: map[string][]gerrit.CommentInfo{
			"main.go": {
				{ID: "c1", Updated: &updated, Message: "needs a nil check", Author: account(8, "bob")},
			},
		},
		NewComments: map[string]bool{"c1": true},
		Transition:  &Transition{From: "NEW", To: "MERGED"},
	}
}

func Test_Filters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		give         Filters
		wantMessages int
		wantVotes    int
		wantComments int
	}{
		{
			name:         "zero filters keep everything",
			give:         Filters{},
			wantMessages: 1,
			wantVotes:    1,
			wantComments: 1,
		},
		{
			name:         "own activity dropped by default",
			give:         Filters{Self: account(8, "bob")},
			wantMessages: 0,
			wantVotes:    1,
			wantComments: 0,
		},
		{
			name:         "own activity kept with include-own",
			give:         Filters{Self: account(8, "bob"), IncludeOwn: true},
			wantMessages: 1,
			wantVotes:    1,
			wantComments: 1,
		},
		{
			name:         "excluded username drops its events only",
			give:         Filters{ExcludeAccounts: []string{"ci"}},
			wantMessages: 1,
			wantVotes:    0,
			wantComments: 1,
		},
		{
			name:         "excluded numeric account id drops its events only",
			give:         Filters{ExcludeAccounts: []string{"8"}},
			wantMessages: 0,
			wantVotes:    1,
			wantComments: 0,
		},
		{
			name:         "content pattern drops matching message, votes pass",
			give:         Filters{ExcludePatterns: []*regexp.Regexp{regexp.MustCompile(`^Build `)}},
			wantMessages: 0,
			wantVotes:    1,
			wantComments: 1,
		},
		{
			name:         "content pattern drops matching comment",
			give:         Filters{ExcludePatterns: []*regexp.Regexp{regexp.MustCompile(`nil check`)}},
			wantMessages: 1,
			wantVotes:    1,
			wantComments: 0,
		},
		{
			name: "composed filters leave the one surviving event",
			give: Filters{
				Self:            account(8, "bob"),
				ExcludeAccounts: []string{"ci"},
			},
			wantMessages: 0,
			wantVotes:    0,
			wantComments: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := filterDelta(t)

			tt.give.apply(d)

			assert.Len(t, d.Messages, tt.wantMessages)
			assert.Len(t, d.Votes, tt.wantVotes)
			assert.Len(t, d.NewComments, tt.wantComments)
			require.NotNil(t, d.Transition, "transitions are never filtered")
			assert.False(t, d.Empty(), "the transition keeps the delta alive")
		})
	}
}

func Test_Filters_FullyFilteredDeltaIsEmpty(t *testing.T) {
	t.Parallel()

	d := filterDelta(t)

	d.Transition = nil

	Filters{
		Self:            account(8, "bob"),
		ExcludeAccounts: []string{"ci"},
	}.apply(d)

	assert.True(t, d.Empty(), "a tick filtered to nothing must produce no notification")
}
