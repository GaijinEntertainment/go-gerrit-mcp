package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_proposals(t *testing.T) {
	t.Parallel()

	files := []string{
		"/COMMIT_MSG",
		"core/scanner.go",
		"core/scanner_test.go",
		"docs/readme.md",
	}

	tests := []struct {
		name string
		give string
		want []string
	}{
		{
			name: "near-miss typo proposes its target",
			give: "core/scaner.go",
			want: []string{"core/scanner.go"},
		},
		{
			name: "case difference matches",
			give: "core/Scanner.go",
			want: []string{"core/scanner.go"},
		},
		{
			name: "wrong prefix matches by path suffix",
			give: "scanner.go",
			want: []string{"core/scanner.go"},
		},
		{
			name: "garbage proposes nothing",
			give: "cmd/server/main.rs",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, proposals(tt.give, files))
		})
	}
}

func Test_proposals_Cap(t *testing.T) {
	t.Parallel()

	got := proposals("item.go", []string{"item1.go", "item2.go", "item3.go", "item4.go"})

	assert.Len(t, got, proposalLimit)
}

func Test_editDistance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give [2]string
		want int
	}{
		{give: [2]string{"", ""}, want: 0},
		{give: [2]string{"abc", "abc"}, want: 0},
		{give: [2]string{"abc", ""}, want: 3},
		{give: [2]string{"kitten", "sitting"}, want: 3},
		{give: [2]string{"scaner", "scanner"}, want: 1},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, editDistance(tt.give[0], tt.give[1]),
			"editDistance(%q, %q)", tt.give[0], tt.give[1])
	}
}
