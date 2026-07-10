package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func secrets() map[string]string {
	return map[string]string{
		"GERRIT_URL":      "https://gerrit.example.com",
		"GERRIT_USERNAME": "bot",
		"GERRIT_TOKEN":    "s3cret",
	}
}

func Test_Load_Groups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		giveArgs []string
		giveEnv  map[string]string
		want     []config.Group
	}{
		{
			name:     "zero config defaults to read",
			giveArgs: nil,
			giveEnv:  secrets(),
			want:     []config.Group{config.GroupRead},
		},
		{
			name:     "env mirror applies when flag absent",
			giveArgs: nil,
			giveEnv: func() map[string]string {
				m := secrets()

				m["GERRIT_MCP_GROUPS"] = "comment"

				return m
			}(),
			want: []config.Group{config.GroupComment},
		},
		{
			name:     "flag wins over env mirror",
			giveArgs: []string{"--groups", "transition"},
			giveEnv: func() map[string]string {
				m := secrets()

				m["GERRIT_MCP_GROUPS"] = "comment"

				return m
			}(),
			want: []config.Group{config.GroupTransition},
		},
		{
			name:     "multiple groups preserve order",
			giveArgs: []string{"--groups", "comment,read"},
			giveEnv:  secrets(),
			want:     []config.Group{config.GroupComment, config.GroupRead},
		},
		{
			name:     "duplicates collapse",
			giveArgs: []string{"--groups", "read,read,comment"},
			giveEnv:  secrets(),
			want:     []config.Group{config.GroupRead, config.GroupComment},
		},
		{
			name:     "whitespace tolerated",
			giveArgs: []string{"--groups", " read , transition "},
			giveEnv:  secrets(),
			want:     []config.Group{config.GroupRead, config.GroupTransition},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(tt.giveArgs, env(tt.giveEnv))
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.Groups)
		})
	}
}

func Test_Load_Errors(t *testing.T) {
	t.Parallel()

	t.Run("missing secrets aggregated", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load(nil, env(map[string]string{}))
		require.Error(t, err)
		assert.ErrorContains(t, err, "GERRIT_URL")
		assert.ErrorContains(t, err, "GERRIT_USERNAME")
		assert.ErrorContains(t, err, "GERRIT_TOKEN")
	})

	t.Run("single missing secret named alone", func(t *testing.T) {
		t.Parallel()

		m := secrets()
		delete(m, "GERRIT_TOKEN")

		_, err := config.Load(nil, env(m))
		require.Error(t, err)
		assert.ErrorContains(t, err, "GERRIT_TOKEN")
		assert.NotContains(t, err.Error(), "GERRIT_URL")
	})

	t.Run("unknown group named in error", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--groups", "read,write"}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "unknown capability groups: write")
	})

	t.Run("empty groups value rejected", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--groups", ""}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "no capability groups enabled")
	})

	t.Run("group error and secret error reported together", func(t *testing.T) {
		t.Parallel()

		m := secrets()
		delete(m, "GERRIT_URL")

		_, err := config.Load([]string{"--groups", "bogus"}, env(m))
		require.Error(t, err)
		assert.ErrorContains(t, err, "GERRIT_URL")
		assert.ErrorContains(t, err, "unknown capability groups: bogus")
	})

	t.Run("unknown flag rejected", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--bogus"}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "parse flags")
	})
}

func Test_Load_Connection(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(nil, env(secrets()))
	require.NoError(t, err)

	assert.Equal(t, "https://gerrit.example.com", cfg.GerritURL)
	assert.Equal(t, "bot", cfg.Username)
	assert.Equal(t, "s3cret", cfg.Token)
}
