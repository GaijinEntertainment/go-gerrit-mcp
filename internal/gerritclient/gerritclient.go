// Package gerritclient wraps the go-gerrit REST client with the concerns the
// library leaves to callers: credential validation at startup and recovery of
// Gerrit error response bodies, which the library's errors discard. All
// Gerrit traffic flows through this package — scoping restrictions hook in
// here.
package gerritclient

import (
	"context"
	"io"
	"net/http"
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
	ErrClientInit   = e.New("create gerrit client")
	ErrCredentials  = e.New("gerrit credential validation")
	errEmptyAccount = e.New("gerrit returned an empty account")
)

// Client is the process-wide authenticated Gerrit API client.
type Client struct {
	gerrit *gerrit.Client
	self   gerrit.AccountInfo
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
		return nil, ErrCredentials.Wrap(errEmptyAccount)
	}

	return &Client{gerrit: g, self: *self}, nil
}

// Self reports the authenticated account as validated at startup.
func (c *Client) Self() gerrit.AccountInfo {
	return c.self
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
		return res
	}

	msg := strings.TrimSpace(string(gerrit.RemoveMagicPrefixLine(body)))
	if msg != "" {
		res = res.WithField("gerrit_message", msg)
	}

	return res
}
