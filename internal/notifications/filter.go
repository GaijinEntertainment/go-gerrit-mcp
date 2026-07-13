package notifications

import (
	"regexp"
	"slices"
	"strconv"

	gerrit "github.com/andygrunwald/go-gerrit"
)

// Filters drop extracted activity before it renders, so excluded events
// never reach the model. Noise control is operator configuration, not server
// heuristics: nothing is filtered by message tag, because a bot's verdict is
// often exactly the awaited outcome (ADR 2.2). Status transitions are never
// filtered — the lifecycle of a subscribed change always reaches the
// session. The zero value filters nothing.
type Filters struct {
	// Self is the authenticated account. Its own activity is dropped unless
	// IncludeOwn — the session's author rarely needs an echo of itself.
	Self gerrit.AccountInfo `exhaustruct:"optional"`
	// IncludeOwn keeps the authenticated account's own activity.
	IncludeOwn bool `exhaustruct:"optional"`
	// ExcludeAccounts lists usernames or numeric account IDs whose activity
	// is dropped.
	ExcludeAccounts []string `exhaustruct:"optional"`
	// ExcludePatterns drop events whose message or comment text matches any
	// of them. Votes carry no text and pass.
	ExcludePatterns []*regexp.Regexp `exhaustruct:"optional"`
}

// apply prunes the delta in place, event by event: messages and comments
// fall to account and content filters, votes to account filters only, the
// transition to none.
func (f Filters) apply(d *Delta) {
	d.Messages = slices.DeleteFunc(d.Messages, func(m gerrit.ChangeMessageInfo) bool {
		return f.accountExcluded(m.Author) || f.contentExcluded(m.Message)
	})

	d.Votes = slices.DeleteFunc(d.Votes, func(v Vote) bool {
		return f.accountExcluded(v.Voter)
	})

	f.pruneComments(d)
}

// pruneComments unmarks new comments whose author or text is excluded; a
// thread none of whose new comments survive is not rendered.
func (f Filters) pruneComments(d *Delta) {
	if len(d.NewComments) == 0 {
		return
	}

	for _, list := range d.Comments {
		for _, ci := range list {
			if !d.NewComments[ci.ID] {
				continue
			}

			if f.accountExcluded(ci.Author) || f.contentExcluded(ci.Message) {
				delete(d.NewComments, ci.ID)
			}
		}
	}
}

// accountExcluded reports whether an event author's activity is configured
// away: the authenticated account itself (unless IncludeOwn), or any entry
// of the exclusion list matched by username or numeric account ID.
func (f Filters) accountExcluded(a gerrit.AccountInfo) bool {
	if !f.IncludeOwn && f.isSelf(a) {
		return true
	}

	for _, entry := range f.ExcludeAccounts {
		if (a.Username != "" && a.Username == entry) || strconv.Itoa(a.AccountID) == entry {
			return true
		}
	}

	return false
}

// isSelf matches by account ID first, username second; a zero Self matches
// nothing.
func (f Filters) isSelf(a gerrit.AccountInfo) bool {
	if f.Self.AccountID != 0 {
		return a.AccountID == f.Self.AccountID
	}

	return f.Self.Username != "" && a.Username == f.Self.Username
}

func (f Filters) contentExcluded(text string) bool {
	for _, re := range f.ExcludePatterns {
		if re.MatchString(text) {
			return true
		}
	}

	return false
}
