package gerritclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errAPICall     = errors.New("api call failed")
	errConnRefused = errors.New("connection refused")
)

func errResponse(status int, body string) *gerrit.Response {
	return &gerrit.Response{
		Response: &http.Response{ //nolint:exhaustruct // synthetic response, only fields apiError reads
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
		},
	}
}

func Test_apiError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		giveStatus int
		giveBody   string
		wantParts  []string
	}{
		{
			name:       "404 plain body",
			giveStatus: http.StatusNotFound,
			giveBody:   "Not found: change 999",
			wantParts:  []string{"status=404", "Not found: change 999", "api call failed"},
		},
		{
			name:       "409 with xssi prefix",
			giveStatus: http.StatusConflict,
			giveBody:   ")]}'\nblocked by Verified",
			wantParts:  []string{"status=409", "blocked by Verified"},
		},
		{
			name:       "empty body keeps status only",
			giveStatus: http.StatusBadRequest,
			giveBody:   "",
			wantParts:  []string{"status=400"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := apiError(errResponse(tt.giveStatus, tt.giveBody), errAPICall)

			require.Error(t, err)

			for _, part := range tt.wantParts {
				assert.ErrorContains(t, err, part)
			}
		})
	}

	t.Run("nil response passes error through", func(t *testing.T) {
		t.Parallel()

		err := apiError(nil, errConnRefused)

		require.Error(t, err)
		assert.ErrorContains(t, err, "connection refused")
	})

	t.Run("oversized body is truncated", func(t *testing.T) {
		t.Parallel()

		huge := strings.Repeat("x", maxErrorBody*2)
		err := apiError(errResponse(http.StatusBadRequest, huge), errAPICall)

		require.Error(t, err)
		assert.LessOrEqual(t, len(err.Error()), maxErrorBody+256)
	})
}
