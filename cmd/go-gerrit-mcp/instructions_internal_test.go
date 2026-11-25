package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals // test binary flag, must register at package init
var update = flag.Bool("update", false, "rewrite golden files with actual output")

// golden asserts that got matches testdata/<name>.golden byte for byte —
// the instructions are model-facing product contract, pinned whole.
func golden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}

	want, err := os.ReadFile(path) //nolint:gosec // path is test-owned: testdata/<name>.golden
	require.NoError(t, err, "missing golden file; regenerate with: go test ./cmd/go-gerrit-mcp/ -update")
	assert.Equal(t, string(want), got)
}

func Test_Instructions(t *testing.T) {
	t.Parallel()

	golden(t, "instructions", instructions)
}
