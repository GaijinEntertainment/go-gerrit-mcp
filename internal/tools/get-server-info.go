package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerState tells whether the server can serve Gerrit.
type ServerState string

// Server states. Every state other than StateReady registers get_server_info
// only.
const (
	StateReady ServerState = "ready"
	// StateNotConfigured: a connection variable is unset or GERRIT_URL does
	// not parse.
	StateNotConfigured ServerState = "not_configured"
	// StateCredentialsRejected: Gerrit answered the credential check with 401
	// or 403.
	StateCredentialsRejected ServerState = "credentials_rejected"
	// StateUnreachable: the credential check failed for any other reason.
	StateUnreachable ServerState = "unreachable"
)

// ServerInfo is what get_server_info reports about the running server.
type ServerInfo struct {
	// Name and Version identify the server build.
	Name    string
	Version string
	// State tells whether the server can serve Gerrit, and Reason why not.
	State  ServerState
	Reason string
	// Account is the username of the authenticated Gerrit account, empty
	// unless the state is StateReady.
	Account string
	// Groups and Tools are the enabled capability groups and the registered
	// Gerrit tools.
	Groups []string
	Tools  []string
	// Projects is the project allowlist, empty when scoping is off.
	Projects []string
}

type serverInfoOutput struct {
	Name            string      `json:"name"`
	Version         string      `json:"version"`
	ProtocolVersion string      `json:"protocol_version"`
	State           ServerState `json:"state"`
	Reason          string      `json:"reason,omitempty"`
	Account         string      `json:"account,omitempty"`
	Groups          []string    `json:"groups"`
	Tools           []string    `json:"tools,omitempty"`
	Projects        []string    `json:"projects,omitempty"`
}

// GetServerInfo is the get_server_info tool. It registers outside the
// capability groups and the tool filters, so a client can always learn which
// server it talks to.
func GetServerInfo(info ServerInfo) Tool {
	return Tool{
		Name: NameGetServerInfo,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name: NameGetServerInfo,
				Description: "Report which go-gerrit-mcp server this is and whether it can serve Gerrit: " +
					"its name and version, the MCP protocol version of this session, its state with the " +
					"reason when it is not ready, the Gerrit account, and the enabled capability groups, " +
					"tools, and project scope. The result is JSON for a program, such as a plugin that " +
					"checks the server before it uses it.",
			}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				out := serverInfoOutput{
					Name:            info.Name,
					Version:         info.Version,
					ProtocolVersion: req.Session.InitializeParams().ProtocolVersion,
					State:           info.State,
					Reason:          info.Reason,
					Account:         info.Account,
					Groups:          info.Groups,
					Tools:           info.Tools,
					Projects:        info.Projects,
				}

				text, _ := json.Marshal(out)

				return textResult(string(text)), nil, nil
			})
		},
	}
}
