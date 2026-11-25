package tools

import (
	"slices"
	"strconv"
	"strings"
	"time"

	gerrit "github.com/andygrunwald/go-gerrit"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

// renderReviewActivity composes the self-sufficient review_activity block:
// nothing in it needs a follow-up fetch to act on. It reuses the vocabulary
// of the read tools: message and vote elements, and comment threads
// byte-identical to get_change_comments.
func renderReviewActivity(d *notifications.Delta) string {
	root := llmxml.NewElement("review_activity",
		llmxml.Attr("change", d.Change.Number),
		llmxml.Attr("project", d.Change.Project),
		llmxml.Attr("status", d.Change.Status),
		llmxml.Attr("updated", timestamp(d.Change.Updated)),
	)

	var children []string

	for _, msg := range d.Messages {
		children = append(children, renderActivityMessage(msg))
	}

	for _, vote := range d.Votes {
		children = append(children, renderActivityVote(vote))
	}

	children = append(children, renderActivityThreads(d)...)

	if d.Transition != nil {
		children = append(children, llmxml.NewElement("transition",
			llmxml.Attr("from", d.Transition.From),
			llmxml.Attr("to", d.Transition.To),
		).String())
	}

	if len(children) > 0 {
		root.WrapText(strings.Join(children, "\n"))
	}

	return root.String()
}

// summarizeReviewActivity states in one line what the review_activity block
// of the delta carries. The line holds counts, label names, vote values, and
// statuses, and no text that a reviewer wrote, so a caller can show it or
// submit it as its own words.
func summarizeReviewActivity(d *notifications.Delta) string {
	var parts []string

	if n := len(d.Messages); n > 0 {
		parts = append(parts, counted(n, "message"))
	}

	for _, vote := range d.Votes {
		value := strconv.Itoa(vote.Value)
		if vote.Value > 0 {
			value = "+" + value
		}

		parts = append(parts, vote.Label+" "+value)
	}

	if n := len(d.NewComments); n > 0 {
		parts = append(parts, counted(n, "comment"))
	}

	if d.Transition != nil {
		parts = append(parts, d.Transition.From+" -> "+d.Transition.To)
	}

	return strings.Join(parts, ", ")
}

func counted(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return strconv.Itoa(n) + " " + noun + "s"
}

// renderInaccessible composes the review_activity block of a change that the
// account can no longer read: there is no fetched change to compose from,
// only the number and the reason.
func renderInaccessible(change int, reason string) string {
	return llmxml.NewElement("review_activity", llmxml.Attr("change", change)).
		WrapText(llmxml.NewElement("inaccessible").InlineText(reason).String()).
		String()
}

// renderActivityMessage mirrors the message element of get_change, extended
// with the tag Gerrit attaches to generated messages.
func renderActivityMessage(msg gerrit.ChangeMessageInfo) string {
	el := llmxml.NewElement("message",
		llmxml.Attr("author", accountLabel(msg.Author)),
		llmxml.Attr("date", timestamp(msg.Date)),
	)

	if msg.RevisionNumber > 0 {
		el.Attr(llmxml.Attr("revision", msg.RevisionNumber))
	}

	if msg.Tag != "" {
		el.Attr(llmxml.Attr("tag", msg.Tag))
	}

	return el.WrapText(msg.Message).String()
}

// renderActivityVote mirrors the vote element of get_change's label
// rendering, extended with the label name and the vote date.
func renderActivityVote(v notifications.Vote) string {
	return llmxml.NewElement("vote",
		llmxml.Attr("label", v.Label),
		llmxml.Attr("value", v.Value),
		llmxml.Attr("by", accountLabel(v.Voter)),
		llmxml.Attr("date", v.Date.UTC().Format(time.RFC3339)),
	).String()
}

// renderActivityThreads renders every thread touched by a new comment as
// get_change_comments renders threads.
func renderActivityThreads(d *notifications.Delta) []string {
	if len(d.NewComments) == 0 {
		return nil
	}

	touched := slices.DeleteFunc(buildThreads(flattenComments(d.Comments, nil, "")), func(t thread) bool {
		return !threadTouched(t, d.NewComments)
	})

	return renderThreadFiles(touched)
}

// threadTouched reports whether any of the thread's comments is new.
func threadTouched(t thread, fresh map[string]bool) bool {
	for _, c := range t.comments {
		if fresh[c.ID] {
			return true
		}
	}

	return false
}
