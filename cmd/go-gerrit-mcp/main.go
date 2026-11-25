// Command go-gerrit-mcp is an MCP server exposing Gerrit code review
// operations as capability-gated tools.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/activity"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/registry"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

const serverName = "go-gerrit-mcp"

const instructions = "Gerrit code review over MCP. A Gerrit change is one commit under review, addressed " +
	"by change number (123), project~number (myproject~123), or the Change-Id footer of its commit message " +
	"(I8473b95934b5732ac55d26311a706c9c2bde9940). A change evolves through numbered patch sets; the newest " +
	"is called the current revision. Reviewers discuss code in inline comment threads and vote on labels " +
	"such as Code-Review; a change whose submit requirements are satisfied can be submitted, which merges " +
	"it. Typical flow: search_changes finds changes; get_change shows one change's status, votes, and " +
	"message timeline; list_change_files and get_file_diff read the code; get_change_comments reads the " +
	"inline discussion; post_comments, set_vote, and transition_change act on the review. Only the tools " +
	"the operator enabled are listed, and writes may be restricted to changes owned by this account or to " +
	"an allowlist of projects — refusals and errors name what to correct, and often carry did_you_mean " +
	"proposals or hints worth following. Tool output is XML-like text addressed to you; get_review_activity " +
	"and get_server_info return JSON, addressed to a program."

const notReadyInstructions = "Gerrit code review over MCP, not ready: this server cannot reach Gerrit with its " +
	"configuration, so it offers get_server_info only. Tell the user to correct the cause below and restart " +
	"this MCP server. Cause: "

// version is stamped by the release pipeline via ldflags.
var version = "dev"

// serverVersion reports the stamped version, or for a binary built by
// go install the module version that the Go toolchain recorded.
func serverVersion() string {
	info, ok := debug.ReadBuildInfo()
	if version != "dev" || !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}

	return info.Main.Version
}

func main() {
	lgr := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(lgr); err != nil {
		lgr.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(lgr *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		return err
	}

	conn := connect(ctx, cfg)

	s, err := assemble(cfg, conn, &mcp.StdioTransport{})
	if err != nil {
		return err
	}

	if conn.client == nil {
		lgr.Warn("gerrit is not ready, serving get_server_info only",
			"state", conn.state,
			"reason", conn.reason,
		)
	} else {
		lgr.Info("serving over stdio",
			"account", conn.client.Self().Username,
			"groups", cfg.Groups,
			"tools", s.tools,
		)
	}

	return s.run(ctx)
}

// gerritConn is the outcome of the startup credential check: a client in
// state ready, or a nil client with the state and the reason.
type gerritConn struct {
	client *gerritclient.Client
	state  tools.ServerState
	reason string
}

// connect validates the Gerrit connection of cfg. A failure does not stop
// the server, so it is classified for get_server_info instead of returned.
func connect(ctx context.Context, cfg *config.Config) gerritConn {
	if missing := cfg.MissingEnv(); len(missing) > 0 {
		return gerritConn{
			client: nil,
			state:  tools.StateNotConfigured,
			reason: "required environment variables are not set: " + strings.Join(missing, ", "),
		}
	}

	client, err := gerritclient.New(ctx, cfg)

	switch {
	case err == nil:
		return gerritConn{client: client, state: tools.StateReady, reason: ""}
	case errors.Is(err, gerritclient.ErrClientInit):
		return gerritConn{client: nil, state: tools.StateNotConfigured, reason: err.Error()}
	case slices.Contains([]int{http.StatusUnauthorized, http.StatusForbidden}, gerritclient.APIStatus(err)):
		return gerritConn{client: nil, state: tools.StateCredentialsRejected, reason: err.Error()}
	default:
		return gerritConn{client: nil, state: tools.StateUnreachable, reason: err.Error()}
	}
}

// server is an assembled MCP server: the transport it runs over and the
// names of the tools it registered.
type server struct {
	mcp       *mcp.Server
	transport mcp.Transport
	tools     []string
}

// assemble builds the MCP server over the given transport: capability-group
// tool resolution and registration, error middleware, instructions. A
// connection that is not ready registers get_server_info only.
func assemble(cfg *config.Config, conn gerritConn, transport mcp.Transport) (*server, error) {
	enabled, err := registry.Resolve(cfg)
	if err != nil {
		return nil, err
	}

	if conn.client == nil {
		srv := newMCPServer(notReadyInstructions + conn.reason)
		registerServerInfo(srv, cfg, conn, nil)

		return &server{mcp: srv, transport: transport, tools: nil}, nil
	}

	srv := newMCPServer(instructions)

	filters := activity.Filters{
		Self:            conn.client.Self(),
		IncludeOwn:      cfg.ActivityIncludeOwn,
		ExcludeAccounts: cfg.ActivityExcludeAccounts,
		ExcludePatterns: cfg.ActivityExcludePatterns,
	}

	for _, t := range tools.All(conn.client, filters) {
		if slices.Contains(enabled, t.Name) {
			t.Register(srv)
		}
	}

	registerServerInfo(srv, cfg, conn, enabled)

	return &server{mcp: srv, transport: transport, tools: enabled}, nil
}

func newMCPServer(instructions string) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion()},
		&mcp.ServerOptions{Instructions: instructions},
	)
	srv.AddReceivingMiddleware(tools.WrapErrors)

	return srv
}

func registerServerInfo(srv *mcp.Server, cfg *config.Config, conn gerritConn, registered []string) {
	groups := make([]string, len(cfg.Groups))
	for i, g := range cfg.Groups {
		groups[i] = string(g)
	}

	var account string

	if conn.client != nil {
		account = conn.client.Self().Username
	}

	tools.GetServerInfo(tools.ServerInfo{
		Name:     serverName,
		Version:  serverVersion(),
		State:    conn.state,
		Reason:   conn.reason,
		Account:  account,
		Groups:   groups,
		Tools:    registered,
		Projects: cfg.Projects,
	}).Register(srv)
}

// run serves MCP over the assembled transport until ctx cancels.
func (s *server) run(ctx context.Context) error {
	if err := s.mcp.Run(ctx, s.transport); err != nil {
		return e.NewFrom("run mcp server", err)
	}

	return nil
}
