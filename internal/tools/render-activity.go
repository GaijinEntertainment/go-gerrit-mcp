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

// NewDeltaRenderer returns the review-notifications payload renderer. It
// lives here rather than in the notifications package because payloads
// compose the vocabulary the read tools already emit — message and vote
// elements, and comment threads byte-identical to get_change_comments.
func NewDeltaRenderer() notifications.Renderer {
	return deltaRenderer{}
}

type deltaRenderer struct{}

// Render composes the self-sufficient review_activity payload plus routing
// meta: nothing in it needs a follow-up fetch to act on.
func (deltaRenderer) Render(d *notifications.Delta) (string, map[string]string) {
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

	content := root.String()
	if len(children) > 0 {
		content = root.WrapText(strings.Join(children, "\n")).String()
	}

	return content, deltaMeta(d)
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

// renderActivityThreads renders every thread touched by a new comment,
// grouped by file exactly as get_change_comments groups them; the thread
// elements themselves come from the same renderer and are byte-identical.
func renderActivityThreads(d *notifications.Delta) []string {
	if len(d.NewComments) == 0 {
		return nil
	}

	threads := buildThreads(flattenComments(d.Comments, nil, ""))

	byFile := map[string][]thread{}

	for _, t := range threads {
		if !threadTouched(t, d.NewComments) {
			continue
		}

		byFile[t.root.path] = append(byFile[t.root.path], t)
	}

	paths := make([]string, 0, len(byFile))
	for path := range byFile {
		paths = append(paths, path)
	}

	slices.SortStableFunc(paths, compareRenderPaths)

	rendered := make([]string, 0, len(paths))

	for _, path := range paths {
		fileThreads := byFile[path]
		slices.SortStableFunc(fileThreads, compareThreads)

		lines := make([]string, 0, len(fileThreads))
		for _, t := range fileThreads {
			lines = append(lines, renderThread(t))
		}

		rendered = append(rendered, llmxml.NewElement("file", llmxml.Attr("path", path)).
			WrapText(strings.Join(lines, "\n")).String())
	}

	return rendered
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

// deltaMeta builds the routing meta of a notification: the change number,
// its project, and the activity kinds the payload carries. Keys stay within
// the channel contract's letters-digits-underscores restriction.
func deltaMeta(d *notifications.Delta) map[string]string {
	var kinds []string

	if len(d.Messages) > 0 {
		kinds = append(kinds, "message")
	}

	if len(d.Votes) > 0 {
		kinds = append(kinds, "vote")
	}

	if len(d.NewComments) > 0 {
		kinds = append(kinds, "comment")
	}

	if d.Transition != nil {
		kinds = append(kinds, "transition")
	}

	return map[string]string{
		"change":  strconv.Itoa(d.Change.Number),
		"project": d.Change.Project,
		"kind":    strings.Join(kinds, ","),
	}
}
