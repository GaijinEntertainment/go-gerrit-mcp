package gerritclient

import (
	"context"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
	gerrit "github.com/andygrunwald/go-gerrit"
)

// Sentinels for change-centric read operations.
var (
	ErrGetChange    = e.New("get change")
	ErrQueryChanges = e.New("query changes")
	ErrListFiles    = e.New("list change files")
	ErrGetDiff      = e.New("get file diff")
	ErrListComments = e.New("list change comments")
)

// CurrentRevision addresses the latest patch set of a change.
const CurrentRevision = "current"

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

// QueryResult is one page of a change query.
type QueryResult struct {
	Changes []gerrit.ChangeInfo
	// More reports whether results exist beyond this page.
	More bool
}

// QueryChanges runs a Gerrit change query. Limit and start page the result;
// More on the returned page signals further results.
func (c *Client) QueryChanges(ctx context.Context, query string, limit, start int) (*QueryResult, error) {
	opt := &gerrit.QueryChangeOptions{}

	opt.Query = []string{query}
	opt.Limit = limit
	opt.Start = start
	opt.AdditionalFields = []string{"DETAILED_ACCOUNTS"}

	res, resp, err := c.gerrit.Changes.QueryChanges(ctx, opt)
	if err != nil {
		return nil, ErrQueryChanges.Wrap(apiError(resp, err), fields.F("query", query))
	}

	if res == nil {
		return nil, ErrQueryChanges.Wrap(errEmptyResponse, fields.F("query", query))
	}

	changes := *res
	more := len(changes) > 0 && changes[len(changes)-1].MoreChanges

	return &QueryResult{Changes: changes, More: more}, nil
}

// ListFiles lists the files of a change revision with diffstat-level data.
// An empty revision addresses the current patch set.
func (c *Client) ListFiles(ctx context.Context, changeID, revision string) (map[string]gerrit.FileInfo, error) {
	if revision == "" {
		revision = CurrentRevision
	}

	files, resp, err := c.gerrit.Changes.ListFiles(ctx, changeID, revision, nil)
	if err != nil {
		return nil, ErrListFiles.Wrap(apiError(resp, err),
			fields.F("change", changeID), fields.F("revision", revision))
	}

	return files, nil
}

// GetDiff fetches the diff of one file in a change revision. An empty
// revision addresses the current patch set.
func (c *Client) GetDiff(ctx context.Context, changeID, revision, path string) (*gerrit.DiffInfo, error) {
	if revision == "" {
		revision = CurrentRevision
	}

	diff, resp, err := c.gerrit.Changes.GetDiff(ctx, changeID, revision, path, nil)
	if err != nil {
		return nil, ErrGetDiff.Wrap(apiError(resp, err),
			fields.F("change", changeID), fields.F("revision", revision), fields.F("file", path))
	}

	if diff == nil {
		return nil, ErrGetDiff.Wrap(errEmptyResponse, fields.F("change", changeID), fields.F("file", path))
	}

	return diff, nil
}

// ListChangeComments lists all published comments of a change across
// revisions, grouped by file path.
func (c *Client) ListChangeComments(ctx context.Context, changeID string) (map[string][]gerrit.CommentInfo, error) {
	comments, resp, err := c.gerrit.Changes.ListChangeComments(ctx, changeID)
	if err != nil {
		return nil, ErrListComments.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if comments == nil {
		return nil, ErrListComments.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	return *comments, nil
}
