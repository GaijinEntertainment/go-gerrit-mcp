package gerritclient

import (
	"context"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
)

// Sentinels for trail-leaving operations.
var (
	ErrSetReview = e.New("set review")

	// ErrOwnChangesOnly reports a trail-leaving operation refused because the
	// change is owned by someone else and the own-changes restriction is on.
	ErrOwnChangesOnly = e.New("change is not owned by the authenticated account " +
		"(own-changes restriction, disable with --own-changes-only=false)")
)

// checkWriteScope gates a trail-leaving operation: the target change must
// pass the project allowlist and, unless foreign changes are allowed, must be
// owned by the authenticated account. The check resolves the change with one
// fetch and always happens before any mutating request leaves the process.
func (c *Client) checkWriteScope(ctx context.Context, changeID string) error {
	if len(c.projects) == 0 && c.allowForeign {
		return nil
	}

	info, resp, err := c.gerrit.Changes.GetChange(ctx, changeID, nil)
	if err != nil {
		return ErrGetChange.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if info == nil {
		return ErrGetChange.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	if !c.projectAllowed(info.Project) {
		return c.scopeError(changeID)
	}

	if !c.allowForeign && info.Owner.AccountID != c.self.AccountID {
		return ErrOwnChangesOnly.WithFields(
			fields.F("change", changeID),
			fields.F("owner", info.Owner.Username),
			fields.F("self", c.self.Username),
		)
	}

	return nil
}

// SetReview posts a review to a change revision: top-level message, inline
// comments, or both. Trail-leaving — gated by project scoping and the
// own-changes restriction.
func (c *Client) SetReview(
	ctx context.Context, changeID, revision string, input *gerrit.ReviewInput,
) (*gerrit.ReviewResult, error) {
	if revision == "" {
		revision = CurrentRevision
	}

	if err := c.checkWriteScope(ctx, changeID); err != nil {
		return nil, ErrSetReview.Wrap(err)
	}

	res, resp, err := c.gerrit.Changes.SetReview(ctx, changeID, revision, input)
	if err != nil {
		return nil, ErrSetReview.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if res == nil {
		return nil, ErrSetReview.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	return res, nil
}
