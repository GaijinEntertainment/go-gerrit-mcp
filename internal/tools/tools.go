// Package tools defines the MCP tools the server exposes. Each tool binds a
// name to a registration closure over the shared Gerrit client; which tools
// actually register is decided by the capability registry.
package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/activity"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/gerritclient"
)

// Tool names, referenced by the capability registry and tool filters.
const (
	NameGetChange         = "get_change"
	NameSearchChanges     = "search_changes"
	NameListChangeFiles   = "list_change_files"
	NameGetFileDiff       = "get_file_diff"
	NameGetChangeComments = "get_change_comments"
	NameGetReviewActivity = "get_review_activity"
	NamePostComments      = "post_comments"
	NameSetVote           = "set_vote"
	NameTransitionChange  = "transition_change"
	NameGetServerInfo     = "get_server_info"
)

// Tool binds a tool name to its MCP registration.
type Tool struct {
	Name     string
	Register func(s *mcp.Server)
}

// All returns every tool definition bound to the given client, in stable
// order. Callers filter by the capability registry before registering. The
// filters apply to the activity that get_review_activity reports.
func All(c *gerritclient.Client, filters activity.Filters) []Tool {
	return []Tool{
		searchChanges(c),
		getChange(c),
		listChangeFiles(c),
		getFileDiff(c),
		getChangeComments(c),
		getReviewActivity(c, filters),
		postComments(c),
		setVote(c),
		transitionChange(c),
	}
}

// textResult wraps rendered llmxml as the sole text content of a result.
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
