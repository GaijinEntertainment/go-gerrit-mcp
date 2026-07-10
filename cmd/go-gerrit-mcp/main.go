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
	"dev.gaijin.team/go/go-gerrit-mcp/internal/registry"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

const serverName = "go-gerrit-mcp"

const instructions = "Gerrit code review over MCP. Tools are gated by capability groups " +
	"(read, comment, transition) configured by the operator; only enabled tools are listed."

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

	srv := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)

	enabled, err := registry.Resolve(cfg)
	if err != nil {
		return err
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

	lgr.Info("serving over stdio",
		"account", client.Self().Username,
		"groups", cfg.Groups,
		"tools", enabled,
	)

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return e.NewFrom("run mcp server", err)
	}

	return nil
}
