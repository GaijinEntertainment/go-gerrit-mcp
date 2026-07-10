package gerritclient

import (
	"context"
	"slices"
	"strings"

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

	// ErrProjectScope reports an operation refused by the project allowlist.
	ErrProjectScope = e.New("change is outside the configured project scope")
)

// CurrentRevision addresses the latest patch set of a change.
const CurrentRevision = "current"

// fieldDetailedAccounts is the o= option filling account name/username on
// owner and reviewer entries.
const fieldDetailedAccounts = "DETAILED_ACCOUNTS"

// changeDetailFields returns the o= options requested for a single-change
// fetch: enough for an agent to understand the review state and for write
// flows to source their identifiers (revision, labels, accounts).
func changeDetailFields() []string {
	return []string{
		"DETAILED_LABELS",
		fieldDetailedAccounts,
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

	if !c.projectAllowed(info.Project) {
		return nil, c.scopeError(id)
	}

	return info, nil
}

// projectAllowed reports whether a project passes the allowlist; an empty
// allowlist admits everything.
func (c *Client) projectAllowed(project string) bool {
	return len(c.projects) == 0 || slices.Contains(c.projects, project)
}

func (c *Client) scopeError(changeID string) error {
	return ErrProjectScope.WithFields(
		fields.F("change", changeID),
		fields.F("projects", strings.Join(c.projects, ",")),
	)
}

// scopedQuery forces the project allowlist into a change query regardless of
// what the caller composed; agent-supplied clauses can only narrow further.
func (c *Client) scopedQuery(query string) string {
	if len(c.projects) == 0 {
		return query
	}

	clauses := make([]string, 0, len(c.projects))
	for _, p := range c.projects {
		clauses = append(clauses, "project:"+p)
	}

	scope := "(" + strings.Join(clauses, " OR ") + ")"

	if strings.TrimSpace(query) == "" {
		return scope
	}

	return scope + " (" + query + ")"
}

// checkProjectScope refuses operations on changes outside the allowlist
// before any request for their content leaves the process. A project~number
// identifier is checked directly; a bare identifier costs one resolving
// fetch.
func (c *Client) checkProjectScope(ctx context.Context, changeID string) error {
	if len(c.projects) == 0 {
		return nil
	}

	if project, _, ok := strings.Cut(changeID, "~"); ok {
		if !c.projectAllowed(project) {
			return c.scopeError(changeID)
		}

		return nil
	}

	info, resp, err := c.gerrit.Changes.GetChange(ctx, changeID, nil)
	if err != nil {
		return ErrGetChange.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if info == nil || !c.projectAllowed(info.Project) {
		return c.scopeError(changeID)
	}

	return nil
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

	opt.Query = []string{c.scopedQuery(query)}
	opt.Limit = limit
	opt.Start = start
	opt.AdditionalFields = []string{fieldDetailedAccounts}

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

	if err := c.checkProjectScope(ctx, changeID); err != nil {
		return nil, ErrListFiles.Wrap(err)
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

	if err := c.checkProjectScope(ctx, changeID); err != nil {
		return nil, ErrGetDiff.Wrap(err)
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
	if err := c.checkProjectScope(ctx, changeID); err != nil {
		return nil, ErrListComments.Wrap(err)
	}

	comments, resp, err := c.gerrit.Changes.ListChangeComments(ctx, changeID)
	if err != nil {
		return nil, ErrListComments.Wrap(apiError(resp, err), fields.F("change", changeID))
	}

	if comments == nil {
		return nil, ErrListComments.Wrap(errEmptyResponse, fields.F("change", changeID))
	}

	return *comments, nil
}
