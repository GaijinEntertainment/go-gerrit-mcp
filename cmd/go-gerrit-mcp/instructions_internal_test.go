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

// Test_Instructions pins the model-facing instructions in both
// configurations: the disabled variant is the zero-config contract and must
// never drift; the enabled variant appends the review-notifications section.
func Test_Instructions(t *testing.T) {
	t.Parallel()

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		golden(t, "instructions-disabled", serverOptions(testConfig(false, 0)).Instructions)
	})

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()

		golden(t, "instructions-enabled", serverOptions(testConfig(true, 0)).Instructions)
	})
}
