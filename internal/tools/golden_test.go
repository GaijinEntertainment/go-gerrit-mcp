package tools_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals // test binary flag, must register at package init
var update = flag.Bool("update", false, "rewrite golden files with actual tool output")

// golden asserts that got matches testdata/<name>.golden byte for byte. Tool
// output is the product contract — the rendered llmxml is exactly what a
// model reads — so renders are pinned whole, not probed for fragments.
// Running the package tests with -update rewrites the files from actual
// output; review the diff before committing.
func golden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}

	want, err := os.ReadFile(path) //nolint:gosec // path is test-owned: testdata/<name>.golden
	require.NoError(t, err, "missing golden file; regenerate with: go test ./internal/tools/ -update")
	assert.Equal(t, string(want), got)
}
