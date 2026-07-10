package gerritclient

import (
	"context"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
)

// Sentinels for change-state transitions.
var (
	ErrSubmitChange  = e.New("submit change")
	ErrAbandonChange = e.New("abandon change")
	ErrRestoreChange = e.New("restore change")
	ErrSetWIP        = e.New("set work in progress")
	ErrSetReady      = e.New("set ready for review")
)

// SubmitChange submits a change (NEW -> MERGED). Gerrit refuses submits that
// fail submit requirements; the refusal reason is carried in the wrapped
// error. Trail-leaving — gated by project scoping and the own-changes
// restriction.
func (c *Client) SubmitChange(ctx context.Context, changeID string) (*gerrit.ChangeInfo, error) {
	if err := c.checkWriteScope(ctx, changeID); err != nil {
		return nil, ErrSubmitChange.Wrap(err)
	}

	info, resp, err := c.gerrit.Changes.SubmitChange(ctx, changeID, &gerrit.SubmitInput{})
	if err != nil {
		return nil, ErrSubmitChange.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if info == nil {
		return nil, ErrSubmitChange.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	return info, nil
}

// AbandonChange abandons a change (NEW -> ABANDONED) with an optional
// message. Trail-leaving — gated by project scoping and the own-changes
// restriction.
func (c *Client) AbandonChange(ctx context.Context, changeID, message string) (*gerrit.ChangeInfo, error) {
	if err := c.checkWriteScope(ctx, changeID); err != nil {
		return nil, ErrAbandonChange.Wrap(err)
	}

	info, resp, err := c.gerrit.Changes.AbandonChange(ctx, changeID, &gerrit.AbandonInput{Message: message})
	if err != nil {
		return nil, ErrAbandonChange.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if info == nil {
		return nil, ErrAbandonChange.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	return info, nil
}

// RestoreChange restores an abandoned change (ABANDONED -> NEW) with an
// optional message. Trail-leaving — gated by project scoping and the
// own-changes restriction.
func (c *Client) RestoreChange(ctx context.Context, changeID, message string) (*gerrit.ChangeInfo, error) {
	if err := c.checkWriteScope(ctx, changeID); err != nil {
		return nil, ErrRestoreChange.Wrap(err)
	}

	info, resp, err := c.gerrit.Changes.RestoreChange(ctx, changeID, &gerrit.RestoreInput{Message: message})
	if err != nil {
		return nil, ErrRestoreChange.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if info == nil {
		return nil, ErrRestoreChange.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	return info, nil
}

// SetWorkInProgress marks a change as work-in-progress with an optional
// message. go-gerrit exposes no /wip endpoint, so the toggle rides a review
// via ReviewInput.work_in_progress — same wire effect. Trail-leaving — gated
// by project scoping and the own-changes restriction (via SetReview).
func (c *Client) SetWorkInProgress(ctx context.Context, changeID, message string) error {
	input := &gerrit.ReviewInput{
		Message:        message,
		WorkInProgress: true,
	}

	if _, err := c.SetReview(ctx, changeID, "", input); err != nil {
		return ErrSetWIP.Wrap(err)
	}

	return nil
}

// SetReadyForReview marks a work-in-progress change as ready for review with
// an optional message. Trail-leaving — gated by project scoping and the
// own-changes restriction.
func (c *Client) SetReadyForReview(ctx context.Context, changeID, message string) error {
	if err := c.checkWriteScope(ctx, changeID); err != nil {
		return ErrSetReady.Wrap(err)
	}

	resp, err := c.gerrit.Changes.SetReadyForReview(ctx, changeID, &gerrit.ReadyForReviewInput{Message: message})
	if err != nil {
		return ErrSetReady.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	return nil
}
