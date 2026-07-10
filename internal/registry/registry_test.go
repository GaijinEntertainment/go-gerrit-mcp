package registry_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/registry"
	"dev.gaijin.team/go/go-gerrit-mcp/internal/tools"
)

func Test_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give []config.Group
		want []string
	}{
		{
			name: "read group exposes get_change",
			give: []config.Group{config.GroupRead},
			want: []string{tools.NameGetChange},
		},
		{
			name: "no groups no tools",
			give: nil,
			want: nil,
		},
		{
			name: "duplicate groups collapse",
			give: []config.Group{config.GroupRead, config.GroupRead},
			want: []string{tools.NameGetChange},
		},
		{
			name: "write groups expose nothing yet",
			give: []config.Group{config.GroupComment, config.GroupTransition},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, registry.Resolve(tt.give))
		})
	}
}
