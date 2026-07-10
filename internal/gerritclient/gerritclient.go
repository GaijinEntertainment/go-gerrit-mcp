// Package gerritclient wraps the go-gerrit REST client with the concerns the
// library leaves to callers: credential validation at startup and recovery of
// Gerrit error response bodies, which the library's errors discard. All
// Gerrit traffic flows through this package — scoping restrictions hook in
// here.
package gerritclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"dev.gaijin.team/go/golib/e"
	gerrit "github.com/andygrunwald/go-gerrit"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
)

const (
	httpTimeout = 30 * time.Second
	// maxErrorBody caps how much of a Gerrit error response is attached to an
	// error; Gerrit error bodies are short plain-text messages.
	maxErrorBody = 4 << 10
)

// Sentinels for the two startup failure classes.
var (
	ErrClientInit    = e.New("create gerrit client")
	ErrCredentials   = e.New("gerrit credential validation")
	errEmptyResponse = e.New("gerrit returned an empty response")
)

// Client is the process-wide authenticated Gerrit API client.
type Client struct {
	gerrit *gerrit.Client
	self   gerrit.AccountInfo
	// projects, when non-empty, confines every operation to the listed
	// Gerrit projects (see docs/glossary.md: Project scoping).
	projects []string
	// allowForeign disables the own-changes restriction on trail-leaving
	// operations (see docs/glossary.md: Own-changes restriction).
	allowForeign bool
}

// New builds an authenticated client and validates the credentials against
// the instance by fetching the calling account. A failure is reported with
// Gerrit's own message where one exists.
func New(ctx context.Context, cfg *config.Config) (*Client, error) {
	g, err := gerrit.NewClient(ctx, cfg.GerritURL, &http.Client{Timeout: httpTimeout})
	if err != nil {
		return nil, ErrClientInit.Wrap(err)
	}

	g.Authentication.SetBasicAuth(cfg.Username, cfg.Token)

	self, resp, err := g.Accounts.GetAccount(ctx, "self")
	if err != nil {
		return nil, ErrCredentials.Wrap(apiError(resp, err))
	}

	if self == nil {
		return nil, ErrCredentials.Wrap(errEmptyResponse)
	}

	return &Client{
		gerrit:       g,
		self:         *self,
		projects:     cfg.Projects,
		allowForeign: cfg.AllowForeignChanges,
	}, nil
}

// Self reports the authenticated account as validated at startup.
func (c *Client) Self() gerrit.AccountInfo {
	return c.self
}

// Projects reports the project allowlist, empty when scoping is off.
func (c *Client) Projects() []string {
	return slices.Clone(c.projects)
}

// apiErr threads the HTTP status of a failed Gerrit call through the error
// chain for programmatic branching; message rendering stays with the wrapped
// error.
type apiErr struct {
	status int
	err    error
}

func (a *apiErr) Error() string { return a.err.Error() }
func (a *apiErr) Unwrap() error { return a.err }

// APIStatus reports the HTTP status carried by an error chain, 0 when the
// error did not originate from a Gerrit API response.
func APIStatus(err error) int {
	var a *apiErr

	if errors.As(err, &a) {
		return a.status
	}

	return 0
}

// apiError converts a go-gerrit response/error pair into an error carrying
// Gerrit's own message. The library's error holds only the status line; the
// response body — readable and unclosed on the error path — holds the reason
// a human can act on.
func apiError(resp *gerrit.Response, err error) error {
	if resp == nil || resp.Body == nil {
		return e.From(err)
	}

	defer func() { _ = resp.Body.Close() }()

	res := e.From(err).WithField("status", resp.StatusCode)

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if readErr != nil {
		return &apiErr{status: resp.StatusCode, err: res}
	}

	msg := strings.TrimSpace(string(gerrit.RemoveMagicPrefixLine(body)))
	if msg != "" {
		res = res.WithField("gerrit_message", msg)
	}

	return &apiErr{status: resp.StatusCode, err: res}
}
