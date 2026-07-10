// Package registry maps enabled capability groups to the tool names they
// expose. Groups are independent and self-sufficient; enabled groups union.
// Tool filters (include/exclude) narrow the resolved set later — they never
// escalate beyond it.
package registry

import (
	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

// groupTools returns the canonical group → tool-name mapping. Write-capable
// groups will additionally bundle the minimal read subset they need to
// function on their own.
func groupTools() map[config.Group][]string {
	return map[config.Group][]string{
		config.GroupRead: {
			tools.NameSearchChanges,
			tools.NameGetChange,
			tools.NameListChangeFiles,
			tools.NameGetFileDiff,
			tools.NameGetChangeComments,
		},
		config.GroupComment:    {}, // arrives with the comment group implementation
		config.GroupTransition: {}, // arrives with the transition group implementation
	}
}

// Resolve returns the tool names exposed by the enabled groups: ordered by
// group input order, deduplicated.
func Resolve(groups []config.Group) []string {
	var (
		names []string
		seen  = map[string]bool{}
	)

	byGroup := groupTools()

	for _, g := range groups {
		for _, name := range byGroup[g] {
			if seen[name] {
				continue
			}

			seen[name] = true

			names = append(names, name)
		}
	}

	return names
}
