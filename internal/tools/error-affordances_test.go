package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
)

// Errors and empty results must carry what the agent needs to recover:
// proposals when the valid set is known, the active scope when it silently
// narrows a query, and the real patch-set range when a revision guess misses.

func Test_SearchChanges_Boundaries(t *testing.T) {
	t.Parallel()

	t.Run("negative start refused before any request", func(t *testing.T) {
		t.Parallel()

		requested := false

		cs := fakeGerritFlag(t, ")]}'\n[]", &requested)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "search_changes",
			Arguments: map[string]any{"query": "status:open", "start": -2},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Contains(t, errorText(t, res), "start must be zero or positive")
		assert.False(t, requested, "no query may leave the process")
	})

	t.Run("empty query without scope refused with a starting point", func(t *testing.T) {
		t.Parallel()

		requested := false

		cs := fakeGerritFlag(t, ")]}'\n[]", &requested)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "search_changes",
			Arguments: map[string]any{"query": "  "},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Contains(t, errorText(t, res), "status:open")
		assert.False(t, requested, "no query may leave the process")
	})

	t.Run("empty query with scope browses the scope", func(t *testing.T) {
		t.Parallel()

		cs, lastURL := scopedGerrit(t, ")]}'\n[]")

		out := callTool(t, cs, "search_changes", map[string]any{"query": ""})

		assert.Equal(t, `<changes query="" scope="core" start="0" count="0" more="false"/>`, out)
		assert.Contains(t, *lastURL, "project:core", "scope clause must reach the wire")
	})

	t.Run("scoped result names the scope", func(t *testing.T) {
		t.Parallel()

		cs, _ := scopedGerrit(t, ")]}'\n[]")

		out := callTool(t, cs, "search_changes", map[string]any{"query": "project:elsewhere"})

		assert.Equal(t,
			`<changes query="project:elsewhere" scope="core" start="0" count="0" more="false"/>`,
			out,
			"the scope attribute is what tells the agent an empty page may mean out-of-scope",
		)
	})
}

func Test_GetFileDiff_UnknownFile(t *testing.T) {
	t.Parallel()

	const emptyDiffJSON = ")]}'\n" + `{"change_type":"MODIFIED","content":[]}`

	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(selfJSON))
		case strings.HasSuffix(r.URL.Path, "/diff"):
			_, _ = w.Write([]byte(emptyDiffJSON))
		case strings.HasSuffix(r.URL.Path, "/files/"):
			_, _ = w.Write([]byte(revisionFilesJSON))
		default:
			_, _ = w.Write([]byte(ownChangeJSON))
		}
	})

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_file_diff",
		Arguments: map[string]any{"change": "123", "file": "core/scaner.go"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "Gerrit's empty diff for an absent file must not render as success")

	golden(t, "error-diff-unknown-file", errorText(t, res))
}

func Test_ListChangeFiles_RevisionNotFound(t *testing.T) {
	t.Parallel()

	const allRevisionsJSON = ")]}'\n" + `{"_number":123,"project":"core",` +
		`"current_revision":"sha3",` +
		`"revisions":{"sha1":{"_number":1},"sha2":{"_number":2},"sha3":{"_number":3}}}`

	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(selfJSON))
		case strings.Contains(r.URL.Path, "/revisions/99/"):
			w.WriteHeader(http.StatusNotFound)

			_, _ = w.Write([]byte("Not found: 99"))

		default:
			_, _ = w.Write([]byte(allRevisionsJSON))
		}
	})

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "list_change_files",
		Arguments: map[string]any{"change": "123", "revision": "99"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)

	text := errorText(t, res)
	assert.Contains(t, text, "Not found: 99")
	assert.Contains(t, text, "valid_patch_sets=1-3")
	assert.Contains(t, text, "current_patch_set=3")
}

// fakeGerritFlag is fakeGerrit with a flag reporting whether any API request
// beyond the startup self-check reached the fake.
func fakeGerritFlag(t *testing.T, fixture string, requested *bool) *mcp.ClientSession {
	t.Helper()

	return session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a/accounts/self" {
			_, _ = w.Write([]byte(selfJSON))

			return
		}

		*requested = true

		_, _ = w.Write([]byte(fixture))
	})
}

// scopedGerrit is fakeGerrit with the project allowlist set to "core".
func scopedGerrit(t *testing.T, fixture string) (*mcp.ClientSession, *string) {
	t.Helper()

	lastURL := new(string)

	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a/accounts/self" {
			_, _ = w.Write([]byte(selfJSON))

			return
		}

		*lastURL = r.URL.String()

		_, _ = w.Write([]byte(fixture))
	}, func(cfg *config.Config) { cfg.Projects = []string{"core"} })

	return cs, lastURL
}
