package tools

import (
	"context"
	"encoding/json"
	"time"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

var (
	errInvalidSince  = e.New("since is not a timestamp that this tool returned")
	errStatusMissing = e.New("status is required together with since")
)

type reviewActivityInput struct {
	Changes []reviewActivityRequest `json:"changes" jsonschema:"Changes to report on"`
}

type reviewActivityRequest struct {
	Change int    `json:"change" jsonschema:"Change number"`
	Since  string `json:"since,omitempty" jsonschema:"The since value of the previous result for this change. Omit it to get the starting point without any activity"`
	Status string `json:"status,omitempty" jsonschema:"The status value of the previous result for this change. Required together with since"`
}

type reviewActivityOutput struct {
	Changes []reviewActivityEntry `json:"changes"`
}

type reviewActivityEntry struct {
	Change  int    `json:"change"`
	URL     string `json:"url"`
	Since   string `json:"since,omitempty"`
	Status  string `json:"status,omitempty"`
	Ended   bool   `json:"ended,omitempty"`
	Summary string `json:"summary,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

func getReviewActivity(c *gerritclient.Client, filters notifications.Filters) Tool {
	return Tool{
		Name: NameGetReviewActivity,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameGetReviewActivity,
				Description: "Report the new review activity on Gerrit changes past a point that the caller " +
					"keeps. The Claude Code review-notifications plugin calls this tool on a timer and " +
					"delivers the activity into the session by itself, so never call it for a change " +
					"that the plugin follows. Without the plugin, call it with all changes and no since " +
					"to get their starting points, then pass back the since and status of each entry " +
					"of the previous result, again in one call. The result is JSON for a program, one " +
					"entry per change: url is the web page " +
					"of the change; content holds the review_activity block with the new change " +
					"messages, votes, inline comment threads, and status transition, and is absent when " +
					"nothing is new; summary states in one line what content carries, without any text " +
					"that a reviewer wrote; ended means stop asking about the change, because it is " +
					"merged, abandoned, or no longer readable; error means the change was not read this " +
					"time, so repeat the same since and status later.",
			}, func(ctx context.Context, _ *mcp.CallToolRequest, in reviewActivityInput,
			) (*mcp.CallToolResult, any, error) {
				reqs, err := activityRequests(in.Changes)
				if err != nil {
					return nil, nil, err
				}

				activities, err := notifications.CollectActivity(ctx, c, filters, reqs)
				if err != nil {
					return nil, nil, err
				}

				out := reviewActivityOutput{Changes: make([]reviewActivityEntry, 0, len(activities))}
				for _, act := range activities {
					out.Changes = append(out.Changes, reviewActivityEntryOf(act, c.ChangeURL(act.Change)))
				}

				text, err := json.Marshal(out)
				if err != nil {
					return nil, nil, e.NewFrom("encode review activity", err)
				}

				return textResult(string(text)), nil, nil
			})
		},
	}
}

func activityRequests(in []reviewActivityRequest) ([]notifications.ActivityRequest, error) {
	reqs := make([]notifications.ActivityRequest, len(in))

	for i, r := range in {
		reqs[i] = notifications.ActivityRequest{Change: r.Change, Since: time.Time{}, Status: r.Status}

		if r.Since == "" {
			continue
		}

		if r.Status == "" {
			return nil, errStatusMissing.WithField("change", r.Change)
		}

		since, err := time.Parse(time.RFC3339Nano, r.Since)
		if err != nil {
			return nil, errInvalidSince.WithFields(fields.F("change", r.Change), fields.F("since", r.Since))
		}

		reqs[i].Since = since
	}

	return reqs, nil
}

func reviewActivityEntryOf(act notifications.Activity, url string) reviewActivityEntry {
	entry := reviewActivityEntry{
		Change:  act.Change,
		URL:     url,
		Since:   "",
		Status:  act.Status,
		Ended:   act.Inaccessible != "" || notifications.IsTerminal(act.Status),
		Summary: "",
		Content: "",
		Error:   "",
	}

	if !act.Updated.IsZero() {
		entry.Since = act.Updated.UTC().Format(time.RFC3339Nano)
	}

	switch {
	case act.Err != nil:
		entry.Error = act.Err.Error()
	case act.Inaccessible != "":
		entry.Summary = act.Inaccessible
		entry.Content = renderInaccessible(act.Change, act.Inaccessible)

	case act.Delta != nil:
		entry.Summary = summarizeReviewActivity(act.Delta)
		entry.Content = renderReviewActivity(act.Delta)

	default:
	}

	return entry
}
