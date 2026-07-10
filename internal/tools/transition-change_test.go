package tools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedPost is the last mutating request the fake Gerrit received.
type recordedPost struct {
	Path string
	Body map[string]any
}

// transitionSession wires a fake Gerrit for the transition flow: self-check,
// change resolve (owned by self), and a recording POST that answers with the
// given response body.
func transitionSession(t *testing.T, postResponse string) (cs *mcp.ClientSession, post *recordedPost) {
	t.Helper()

	post = &recordedPost{Path: "", Body: nil}

	cs = session(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/a/accounts/self":
			_, _ = w.Write([]byte(selfJSON))

		case r.Method == http.MethodPost:
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read transition body: %v", err)

				return
			}

			post.Path = r.URL.Path

			if err := json.Unmarshal(raw, &post.Body); err != nil {
				t.Errorf("unmarshal transition body: %v", err)

				return
			}

			_, _ = w.Write([]byte(postResponse))

		default:
			_, _ = w.Write([]byte(ownChangeJSON))
		}
	})

	return cs, post
}

func Test_TransitionChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		giveAction   string
		giveMessage  string
		giveResponse string
		wantPath     string
		wantBody     map[string]any
		wantAck      string
	}{
		{
			name:         "submit maps to submit endpoint and reports status",
			giveAction:   "submit",
			giveMessage:  "",
			giveResponse: ")]}'\n" + `{"_number":123,"status":"MERGED"}`,
			wantPath:     "/a/changes/123/submit",
			wantBody:     map[string]any{},
			wantAck:      `<change_transitioned change="123" action="submit" status="MERGED"/>`,
		},
		{
			name:         "abandon carries the message",
			giveAction:   "abandon",
			giveMessage:  "superseded",
			giveResponse: ")]}'\n" + `{"_number":123,"status":"ABANDONED"}`,
			wantPath:     "/a/changes/123/abandon",
			wantBody:     map[string]any{"message": "superseded"},
			wantAck:      `<change_transitioned change="123" action="abandon" status="ABANDONED"/>`,
		},
		{
			name:         "restore carries the message",
			giveAction:   "restore",
			giveMessage:  "still needed",
			giveResponse: ")]}'\n" + `{"_number":123,"status":"NEW"}`,
			wantPath:     "/a/changes/123/restore",
			wantBody:     map[string]any{"message": "still needed"},
			wantAck:      `<change_transitioned change="123" action="restore" status="NEW"/>`,
		},
		{
			name:         "wip rides a review with work_in_progress",
			giveAction:   "WIP",
			giveMessage:  "parking",
			giveResponse: ")]}'\n{}",
			wantPath:     "/a/changes/123/revisions/current/review",
			wantBody:     map[string]any{"message": "parking", "work_in_progress": true},
			wantAck:      `<change_transitioned change="123" action="wip"/>`,
		},
		{
			name:         "ready maps to ready endpoint",
			giveAction:   "ready",
			giveMessage:  "ptal",
			giveResponse: ")]}'\n{}",
			wantPath:     "/a/changes/123/ready",
			wantBody:     map[string]any{"message": "ptal"},
			wantAck:      `<change_transitioned change="123" action="ready"/>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cs, post := transitionSession(t, tt.giveResponse)

			args := map[string]any{"change": "123", "action": tt.giveAction}
			if tt.giveMessage != "" {
				args["message"] = tt.giveMessage
			}

			out := callTool(t, cs, "transition_change", args)

			assert.Contains(t, out, tt.wantAck)
			assert.Equal(t, tt.wantPath, post.Path)
			assert.Equal(t, tt.wantBody, post.Body)
		})
	}
}

func Test_TransitionChange_Refusals(t *testing.T) {
	t.Parallel()

	t.Run("unknown action refused", func(t *testing.T) {
		t.Parallel()

		cs, post := transitionSession(t, ")]}'\n{}")

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "transition_change",
			Arguments: map[string]any{"change": "123", "action": "merge"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "unknown action")

		assert.Empty(t, post.Path, "no mutating request may leave the process")
	})

	t.Run("submit with message refused", func(t *testing.T) {
		t.Parallel()

		cs, post := transitionSession(t, ")]}'\n{}")

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "transition_change",
			Arguments: map[string]any{"change": "123", "action": "submit", "message": "ship it"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "submit does not accept a message")

		assert.Empty(t, post.Path, "no mutating request may leave the process")
	})

	t.Run("blocked submit surfaces gerrit's reason verbatim", func(t *testing.T) {
		t.Parallel()

		cs := session(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/a/accounts/self":
				_, _ = w.Write([]byte(selfJSON))

			case r.Method == http.MethodPost:
				w.WriteHeader(http.StatusConflict)

				_, _ = w.Write([]byte("submit requirement 'Code-Review' is unsatisfied"))

			default:
				_, _ = w.Write([]byte(ownChangeJSON))
			}
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "transition_change",
			Arguments: map[string]any{"change": "123", "action": "submit"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "submit requirement 'Code-Review' is unsatisfied")
	})

	t.Run("foreign change refused by own-changes restriction", func(t *testing.T) {
		t.Parallel()

		posted := false

		cs := session(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/a/accounts/self":
				_, _ = w.Write([]byte(selfJSON))

			case r.Method == http.MethodPost:
				posted = true

				_, _ = w.Write([]byte(")]}'\n{}"))

			default:
				_, _ = w.Write([]byte(foreignChangeJSON))
			}
		})

		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "transition_change",
			Arguments: map[string]any{"change": "123", "action": "abandon"},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)

		text, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "own-changes")

		assert.False(t, posted, "no mutating request may leave the process")
	})
}
