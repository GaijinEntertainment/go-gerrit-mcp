package gerritclient_test

import (
	"encoding/json"
	"testing"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_Learning_ReviewInputWire pins the go-gerrit ReviewInput/CommentInput
// wire shapes the comment flow is built on: field names must match the
// documented Gerrit REST entities, the unresolved tri-state must distinguish
// absent from false, and unset optional fields must stay off the wire.
func Test_Learning_ReviewInputWire(t *testing.T) {
	t.Parallel()

	unresolvedTrue := true
	unresolvedFalse := false

	input := gerrit.ReviewInput{
		Message: "overall message",
		Notify:  "OWNER",
		Labels:  map[string]int{"Code-Review": -1},
		Comments: map[string][]gerrit.CommentInput{
			"core/scanner.go": {
				{
					Message:    "reply",
					InReplyTo:  "abc123",
					Unresolved: &unresolvedFalse,
				},
				{
					Message: "range comment",
					Range: &gerrit.CommentRange{
						StartLine:      10,
						StartCharacter: 0,
						EndLine:        12,
						EndCharacter:   0,
					},
					Unresolved: &unresolvedTrue,
				},
			},
		},
	}

	raw, err := json.Marshal(input)
	require.NoError(t, err)

	var wire map[string]any

	require.NoError(t, json.Unmarshal(raw, &wire))

	assert.Equal(t, "overall message", wire["message"])
	assert.Equal(t, "OWNER", wire["notify"])
	assert.Equal(t, map[string]any{"Code-Review": float64(-1)}, wire["labels"])

	comments, ok := wire["comments"].(map[string]any)
	require.True(t, ok)

	entries, ok := comments["core/scanner.go"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 2)

	reply, ok := entries[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "abc123", reply["in_reply_to"])
	assert.Equal(t, false, reply["unresolved"])
	assert.NotContains(t, reply, "range")
	assert.NotContains(t, reply, "line")

	ranged, ok := entries[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, ranged["unresolved"])
	assert.Equal(t, map[string]any{
		"start_line":      float64(10),
		"start_character": float64(0),
		"end_line":        float64(12),
		"end_character":   float64(0),
	}, ranged["range"])
	assert.NotContains(t, ranged, "in_reply_to")

	// Absent tri-state stays off the wire entirely.
	bare := gerrit.CommentInput{Message: "plain"}

	raw, err = json.Marshal(bare)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "unresolved")
}
