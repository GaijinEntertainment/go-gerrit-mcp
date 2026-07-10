// Package registry maps enabled capability groups to the tool names they
// expose. Groups are independent and self-sufficient; enabled groups union.
// Tool filters (include/exclude) narrow the resolved set — they never
// escalate beyond it.
package registry

import (
	"strings"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

// ErrUnknownTools reports filter entries that name no known tool.
var ErrUnknownTools = e.New("unknown tool names in filters")

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
		// comment bundles the minimal read subset it needs: understanding the
		// change and the threads it replies to.
		config.GroupComment: {
			tools.NameGetChange,
			tools.NameGetChangeComments,
			tools.NamePostComments,
		},
		// transition bundles the minimal read subset it needs: understanding
		// the change whose state it moves.
		config.GroupTransition: {
			tools.NameGetChange,
			tools.NameSetVote,
			tools.NameTransitionChange,
		},
	}
}

// Resolve returns the tool names the configuration exposes: enabled groups
// union (ordered by group input order, deduplicated), narrowed by the
// include and exclude filters. Filter entries naming no known tool fail
// resolution so misconfigurations surface at startup.
func Resolve(cfg *config.Config) ([]string, error) {
	if err := validateFilters(cfg); err != nil {
		return nil, err
	}

	var (
		names []string
		seen  = map[string]bool{}
	)

	byGroup := groupTools()

	include := toSet(cfg.IncludeTools)
	exclude := toSet(cfg.ExcludeTools)

	for _, g := range cfg.Groups {
		for _, name := range byGroup[g] {
			if seen[name] || exclude[name] {
				continue
			}

			if len(include) > 0 && !include[name] {
				continue
			}

			seen[name] = true

			names = append(names, name)
		}
	}

	return names, nil
}

func validateFilters(cfg *config.Config) error {
	known := map[string]bool{}
	for _, names := range groupTools() {
		for _, name := range names {
			known[name] = true
		}
	}

	var unknown []string

	for _, name := range append(append([]string{}, cfg.IncludeTools...), cfg.ExcludeTools...) {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}

	if len(unknown) > 0 {
		return ErrUnknownTools.WithFields(fields.F("tools", strings.Join(unknown, ",")))
	}

	return nil
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}

	return set
}
