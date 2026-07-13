// Command go-gerrit-mcp is an MCP server exposing Gerrit code review
// operations as capability-gated tools.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"dev.gaijin.team/go/golib/e"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
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
	"proposals or hints worth following. All tool output is XML-like text addressed to you."

// notificationsInstructions is appended to the instructions only when review
// notifications are enabled; the zero-config instructions stay byte-identical.
const notificationsInstructions = " Review notifications are enabled: after pushing or picking up a change " +
	"whose review you must follow, call subscribe_change once — its review activity then arrives in this " +
	"session automatically, no polling needed."

// version is stamped by the release pipeline via ldflags.
var version = "dev"

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

	client, err := gerritclient.New(ctx, cfg)
	if err != nil {
		return err
	}

	s, err := assemble(cfg, client, &mcp.StdioTransport{}, lgr)
	if err != nil {
		return err
	}

	lgr.Info("serving over stdio",
		"account", client.Self().Username,
		"groups", cfg.Groups,
		"tools", s.tools,
	)

	return s.run(ctx)
}

// server is an assembled MCP server: the transport it runs over, the names
// of the tools it registered, and the notifications poller when review
// notifications are enabled.
type server struct {
	mcp       *mcp.Server
	transport mcp.Transport
	tools     []string
	poller    *notifications.Poller
}

// assemble builds the MCP server over the given transport: capability-group
// tool resolution and registration, error middleware, instructions. With
// review notifications enabled it additionally declares the channel
// capability, registers subscribe_change, appends the instructions sentence,
// and wires the poller through the connection-capturing transport; disabled,
// the assembled server is byte-identical to the historical output and
// starts no goroutine.
func assemble(
	cfg *config.Config, client *gerritclient.Client, transport mcp.Transport, lgr *slog.Logger,
) (*server, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: version}, serverOptions(cfg))
	srv.AddReceivingMiddleware(tools.WrapErrors)

	enabled, err := registry.Resolve(cfg)
	if err != nil {
		return nil, err
	}

	set := make(map[string]bool, len(enabled))
	for _, name := range enabled {
		set[name] = true
	}

	for _, t := range tools.All(client) {
		if set[t.Name] {
			t.Register(srv)
		}
	}

	s := &server{mcp: srv, transport: transport, tools: enabled, poller: nil}

	if cfg.ReviewNotifications {
		capture := &captureTransport{inner: transport}
		store := notifications.NewStore()
		emitter := &channelEmitter{transport: capture, lgr: lgr}

		tools.SubscribeChange(client, store).Register(srv)
		tools.UnsubscribeChange(client, store).Register(srv)

		s.transport = capture
		s.tools = append(s.tools, tools.NameSubscribeChange, tools.NameUnsubscribeChange)
		s.poller = notifications.NewPoller(
			store, client, tools.NewDeltaRenderer(), emitter, cfg.ReviewNotificationsPollInterval, lgr,
		)
	}

	return s, nil
}

// serverOptions carries the instructions and, only when review notifications
// are enabled, the channel capability. Overriding Capabilities keeps the
// SDK's tools inference but drops its logging default, so Logging is
// declared explicitly to keep parity with the disabled path (pinned by
// Test_Learning_CapabilitiesOverride).
func serverOptions(cfg *config.Config) *mcp.ServerOptions {
	opts := &mcp.ServerOptions{Instructions: instructions}

	if cfg.ReviewNotifications {
		opts.Instructions += notificationsInstructions

		opts.Capabilities = &mcp.ServerCapabilities{
			Logging:      &mcp.LoggingCapabilities{},
			Experimental: map[string]any{channelCapability: map[string]any{}},
		}
	}

	return opts
}

// run serves MCP over the assembled transport until ctx cancels, with the
// poller — when one was assembled — running alongside on the same context.
func (s *server) run(ctx context.Context) error {
	if s.poller != nil {
		go s.poller.Run(ctx)
	}

	if err := s.mcp.Run(ctx, s.transport); err != nil {
		return e.NewFrom("run mcp server", err)
	}

	return nil
}
