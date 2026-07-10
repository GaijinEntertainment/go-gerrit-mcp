package registry_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/registry"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

func cfg(groups []config.Group, include, exclude []string) *config.Config {
	return &config.Config{
		GerritURL:           "https://gerrit.example.com",
		Username:            "bot",
		Token:               "s3cret",
		Groups:              groups,
		IncludeTools:        include,
		ExcludeTools:        exclude,
		Projects:            nil,
		AllowForeignChanges: false,
	}
}

func allReadTools() []string {
	return []string{
		tools.NameSearchChanges,
		tools.NameGetChange,
		tools.NameListChangeFiles,
		tools.NameGetFileDiff,
		tools.NameGetChangeComments,
	}
}

func Test_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		giveGroups  []config.Group
		giveInclude []string
		giveExclude []string
		want        []string
	}{
		{
			name:        "read group exposes the change-read tools",
			giveGroups:  []config.Group{config.GroupRead},
			giveInclude: nil,
			giveExclude: nil,
			want:        allReadTools(),
		},
		{
			name:        "no groups no tools",
			giveGroups:  nil,
			giveInclude: nil,
			giveExclude: nil,
			want:        nil,
		},
		{
			name:        "duplicate groups collapse",
			giveGroups:  []config.Group{config.GroupRead, config.GroupRead},
			giveInclude: nil,
			giveExclude: nil,
			want:        allReadTools(),
		},
		{
			name:        "comment group bundles its read subset",
			giveGroups:  []config.Group{config.GroupComment},
			giveInclude: nil,
			giveExclude: nil,
			want: []string{
				tools.NameGetChange,
				tools.NameGetChangeComments,
				tools.NamePostComments,
			},
		},
		{
			name:        "read and comment union deduplicates",
			giveGroups:  []config.Group{config.GroupRead, config.GroupComment},
			giveInclude: nil,
			giveExclude: nil,
			want:        append(allReadTools(), tools.NamePostComments),
		},
		{
			name:        "transition group exposes nothing yet",
			giveGroups:  []config.Group{config.GroupTransition},
			giveInclude: nil,
			giveExclude: nil,
			want:        nil,
		},
		{
			name:        "exclude removes tools",
			giveGroups:  []config.Group{config.GroupRead},
			giveInclude: nil,
			giveExclude: []string{tools.NameGetFileDiff, tools.NameSearchChanges},
			want: []string{
				tools.NameGetChange,
				tools.NameListChangeFiles,
				tools.NameGetChangeComments,
			},
		},
		{
			name:        "include keeps only the listed subset",
			giveGroups:  []config.Group{config.GroupRead},
			giveInclude: []string{tools.NameGetChange, tools.NameGetChangeComments},
			giveExclude: nil,
			want: []string{
				tools.NameGetChange,
				tools.NameGetChangeComments,
			},
		},
		{
			name:        "exclude wins over include",
			giveGroups:  []config.Group{config.GroupRead},
			giveInclude: []string{tools.NameGetChange, tools.NameGetChangeComments},
			giveExclude: []string{tools.NameGetChangeComments},
			want:        []string{tools.NameGetChange},
		},
		{
			name:        "include never escalates beyond enabled groups",
			giveGroups:  []config.Group{config.GroupComment},
			giveInclude: []string{tools.NameSearchChanges},
			giveExclude: nil,
			want:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := registry.Resolve(cfg(tt.giveGroups, tt.giveInclude, tt.giveExclude))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Resolve_UnknownFilterNames(t *testing.T) {
	t.Parallel()

	t.Run("unknown include name fails", func(t *testing.T) {
		t.Parallel()

		_, err := registry.Resolve(cfg([]config.Group{config.GroupRead}, []string{"bogus_tool"}, nil))
		require.Error(t, err)
		require.ErrorIs(t, err, registry.ErrUnknownTools)
		assert.ErrorContains(t, err, "bogus_tool")
	})

	t.Run("unknown exclude name fails", func(t *testing.T) {
		t.Parallel()

		_, err := registry.Resolve(cfg([]config.Group{config.GroupRead}, nil, []string{"nope"}))
		require.Error(t, err)
		require.ErrorIs(t, err, registry.ErrUnknownTools)
		assert.ErrorContains(t, err, "nope")
	})
}
