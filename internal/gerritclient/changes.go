package gerritclient

import (
	"context"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
)

// ErrGetChange reports a failed change fetch.
var ErrGetChange = e.New("get change")

// changeDetailFields returns the o= options requested for a single-change
// fetch: enough for an agent to understand the review state and for write
// flows to source their identifiers (revision, labels, accounts).
func changeDetailFields() []string {
	return []string{
		"DETAILED_LABELS",
		"DETAILED_ACCOUNTS",
		"CURRENT_REVISION",
		"MESSAGES",
		"SUBMITTABLE",
	}
}

// GetChange fetches a change with review-relevant detail. The id is any
// Gerrit change identifier: numeric, project~number, or Change-Id.
func (c *Client) GetChange(ctx context.Context, id string) (*gerrit.ChangeInfo, error) {
	opt := &gerrit.ChangeOptions{AdditionalFields: changeDetailFields()}

	info, resp, err := c.gerrit.Changes.GetChange(ctx, id, opt)
	if err != nil {
		return nil, ErrGetChange.Wrap(apiError(resp, err), fields.F("change", id))
	}

	if info == nil {
		return nil, ErrGetChange.Wrap(errEmptyResponse, fields.F("change", id))
	}

	return info, nil
}
