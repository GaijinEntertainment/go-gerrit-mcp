package tools_test

import (
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGerrit routes the self-check plus one API fixture; it records the last
// API request URL for wire assertions.
func fakeGerrit(t *testing.T, fixture string) (cs *mcp.ClientSession, lastURL *string) {
	t.Helper()

	lastURL = new(string)

	cs = session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a/accounts/self" {
			_, _ = w.Write([]byte(selfJSON))

			return
		}

		*lastURL = r.URL.String()

		_, _ = w.Write([]byte(fixture))
	})

	return cs, lastURL
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)

	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	return text.Text
}

func Test_SearchChanges(t *testing.T) {
	t.Parallel()

	const fixture = ")]}'\n" + `[
	  {"_number": 1, "project": "core", "branch": "main", "subject": "First", "status": "NEW",
	   "updated": "2026-07-01 10:00:00.000000000",
	   "owner": {"_account_id": 7, "name": "Alice", "username": "alice"}},
	  {"_number": 2, "project": "core", "branch": "main", "subject": "Second", "status": "MERGED",
	   "updated": "2026-07-02 10:00:00.000000000",
	   "owner": {"_account_id": 8, "name": "Bob", "username": "bob"},
	   "_more_changes": true}
	]`

	cs, lastURL := fakeGerrit(t, fixture)

	out := callTool(t, cs, "search_changes", map[string]any{"query": "status:open owner:self"})

	golden(t, "search-changes", out)

	assert.Contains(t, *lastURL, "q=status")
	assert.Contains(t, *lastURL, "n=25", "default limit applies")
	assert.Contains(t, *lastURL, "DETAILED_ACCOUNTS")
}

func Test_ListChangeFiles(t *testing.T) {
	t.Parallel()

	const fixture = ")]}'\n" + `{
	  "/COMMIT_MSG": {"status": "A", "lines_inserted": 9, "size_delta": 200, "size": 200},
	  "core/scanner.go": {"lines_inserted": 5, "lines_deleted": 2, "size_delta": 90, "size": 1024},
	  "assets/logo.png": {"status": "A", "binary": true, "size_delta": 4096, "size": 4096},
	  "core/renamed.go": {"status": "R", "old_path": "core/old.go", "lines_inserted": 1, "size_delta": 10, "size": 300}
	}`

	cs, lastURL := fakeGerrit(t, fixture)

	out := callTool(t, cs, "list_change_files", map[string]any{"change": "123"})

	golden(t, "list-change-files", out)

	assert.Contains(t, *lastURL, "/a/changes/123/revisions/current/files")
}

func Test_GetFileDiff(t *testing.T) {
	t.Parallel()

	t.Run("text diff with prefixes and skip marker", func(t *testing.T) {
		t.Parallel()

		const fixture = ")]}'\n" + `{
		  "change_type": "MODIFIED",
		  "diff_header": ["diff --git a/core/scanner.go b/core/scanner.go"],
		  "content": [
		    {"skip": 40},
		    {"ab": ["func scan() {"]},
		    {"a": ["\told := 1"], "b": ["\tnew := 2", "\textra := 3"]},
		    {"ab": ["}"]}
		  ]
		}`

		cs, lastURL := fakeGerrit(t, fixture)

		out := callTool(t, cs, "get_file_diff", map[string]any{
			"change": "123", "file": "core/scanner.go", "revision": "2",
		})

		golden(t, "file-diff-text", out)

		assert.Contains(t, *lastURL, "/a/changes/123/revisions/2/files/core%2Fscanner.go/diff")
	})

	t.Run("binary diff has no body", func(t *testing.T) {
		t.Parallel()

		const fixture = ")]}'\n" + `{"change_type": "ADDED", "binary": true, "content": []}`

		cs, _ := fakeGerrit(t, fixture)

		out := callTool(t, cs, "get_file_diff", map[string]any{"change": "123", "file": "logo.png"})

		golden(t, "file-diff-binary", out)
	})
}

func Test_GetChangeComments(t *testing.T) {
	t.Parallel()

	const fixture = ")]}'\n" + `{
	  "core/scanner.go": [
	    {"id": "c1", "line": 10, "patch_set": 1, "message": "Is this nil-safe?", "unresolved": true,
	     "updated": "2026-07-01 10:00:00.000000000",
	     "author": {"_account_id": 8, "name": "Bob", "username": "bob"}},
	    {"id": "c2", "in_reply_to": "c1", "line": 10, "patch_set": 1, "message": "Fixed in ps2",
	     "unresolved": false,
	     "updated": "2026-07-01 11:00:00.000000000",
	     "author": {"_account_id": 7, "name": "Alice", "username": "alice"}},
	    {"id": "c3", "range": {"start_line": 20, "end_line": 25}, "patch_set": 2,
	     "message": "This block races", "unresolved": true,
	     "updated": "2026-07-02 09:00:00.000000000",
	     "author": {"_account_id": 8, "name": "Bob", "username": "bob"}}
	  ],
	  "docs/readme.md": [
	    {"id": "c4", "in_reply_to": "gone", "line": 1, "message": "Orphan reply",
	     "updated": "2026-07-01 12:00:00.000000000",
	     "author": {"_account_id": 7, "name": "Alice", "username": "alice"}}
	  ]
	}`

	t.Run("threads with resolution state", func(t *testing.T) {
		t.Parallel()

		cs, lastURL := fakeGerrit(t, fixture)

		out := callTool(t, cs, "get_change_comments", map[string]any{"change": "123"})

		golden(t, "change-comments-all", out)

		assert.Contains(t, *lastURL, "/a/changes/123/comments")
	})

	t.Run("unresolved filter drops resolved threads", func(t *testing.T) {
		t.Parallel()

		cs, _ := fakeGerrit(t, fixture)

		out := callTool(t, cs, "get_change_comments", map[string]any{"change": "123", "status": "unresolved"})

		golden(t, "change-comments-unresolved", out)
	})

	t.Run("invalid filter is an error", func(t *testing.T) {
		t.Parallel()

		cs, _ := fakeGerrit(t, fixture)

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "get_change_comments",
			Arguments: map[string]any{"change": "123", "status": "bogus"},
		})
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
}
