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

	t.Run("unknown group named in error", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--groups", "read,write"}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "unknown capability groups")
		assert.ErrorContains(t, err, "write")
	})

	t.Run("empty groups value rejected", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--groups", ""}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "no capability groups enabled")
	})

	t.Run("unknown flag rejected", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--bogus"}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "parse flags")
	})
}

func Test_Load_Lists(t *testing.T) {
	t.Parallel()

	t.Run("filters and projects parse from flags", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load([]string{
			"--include-tools", "get_change, search_changes",
			"--exclude-tools", "get_file_diff",
			"--projects", " core ,infra, ",
		}, env(secrets()))
		require.NoError(t, err)

		assert.Equal(t, []string{"get_change", "search_changes"}, cfg.IncludeTools)
		assert.Equal(t, []string{"get_file_diff"}, cfg.ExcludeTools)
		assert.Equal(t, []string{"core", "infra"}, cfg.Projects)
	})

	t.Run("lists resolve from env mirrors", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_PROJECTS"] = "core"
		m["GERRIT_MCP_EXCLUDE_TOOLS"] = "search_changes"

		cfg, err := config.Load(nil, env(m))
		require.NoError(t, err)

		assert.Equal(t, []string{"core"}, cfg.Projects)
		assert.Equal(t, []string{"search_changes"}, cfg.ExcludeTools)
		assert.Nil(t, cfg.IncludeTools)
	})
}

func Test_Load_OwnChanges(t *testing.T) {
	t.Parallel()

	t.Run("restriction on by default", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(nil, env(secrets()))
		require.NoError(t, err)
		assert.False(t, cfg.AllowForeignChanges)
	})

	t.Run("flag disables restriction", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load([]string{"--own-changes-only=false"}, env(secrets()))
		require.NoError(t, err)
		assert.True(t, cfg.AllowForeignChanges)
	})

	t.Run("env mirror disables restriction", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_OWN_CHANGES_ONLY"] = "false"

		cfg, err := config.Load(nil, env(m))
		require.NoError(t, err)
		assert.True(t, cfg.AllowForeignChanges)
	})

	t.Run("flag wins over env mirror", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_OWN_CHANGES_ONLY"] = "false"

		cfg, err := config.Load([]string{"--own-changes-only=true"}, env(m))
		require.NoError(t, err)
		assert.False(t, cfg.AllowForeignChanges)
	})

	t.Run("invalid bool reported with other errors", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{
			"--own-changes-only=nope", "--groups", "bogus",
		}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid boolean flag value")
		assert.ErrorContains(t, err, "own-changes-only")
		assert.ErrorContains(t, err, "unknown capability groups")
	})
}

func Test_Load_ActivityFilters(t *testing.T) {
	t.Parallel()

	t.Run("former review-notifications flags are refused", func(t *testing.T) {
		t.Parallel()

		for _, flag := range []string{
			"--review-notifications-include-own=true",
			"--review-notifications-exclude-accounts=ci-bot",
			"--review-notifications-exclude-patterns=^Build",
		} {
			_, err := config.Load([]string{flag}, env(secrets()))
			assert.ErrorContains(t, err, "parse flags", flag)
		}
	})

	t.Run("former review-notifications env mirror is ignored", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_REVIEW_NOTIFICATIONS_INCLUDE_OWN"] = "true"

		cfg, err := config.Load(nil, env(m))
		require.NoError(t, err)
		assert.False(t, cfg.ActivityIncludeOwn)
	})

	t.Run("zero config resolves to no-op filters", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(nil, env(secrets()))
		require.NoError(t, err)
		assert.False(t, cfg.ActivityIncludeOwn)
		assert.Nil(t, cfg.ActivityExcludeAccounts)
		assert.Nil(t, cfg.ActivityExcludePatterns)
	})

	t.Run("include-own resolves from flag", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load([]string{"--activity-include-own", "true"}, env(secrets()))
		require.NoError(t, err)
		assert.True(t, cfg.ActivityIncludeOwn)
	})

	t.Run("include-own flag wins over env mirror", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_ACTIVITY_INCLUDE_OWN"] = "true"

		cfg, err := config.Load([]string{"--activity-include-own=false"}, env(m))
		require.NoError(t, err)
		assert.False(t, cfg.ActivityIncludeOwn)
	})

	t.Run("exclude-accounts parses from flag with whitespace", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load([]string{
			"--activity-exclude-accounts", " ci-bot , 1000042 ",
		}, env(secrets()))
		require.NoError(t, err)
		assert.Equal(t, []string{"ci-bot", "1000042"}, cfg.ActivityExcludeAccounts)
	})

	t.Run("exclude-accounts resolves from env mirror", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_ACTIVITY_EXCLUDE_ACCOUNTS"] = "ci-bot"

		cfg, err := config.Load(nil, env(m))
		require.NoError(t, err)
		assert.Equal(t, []string{"ci-bot"}, cfg.ActivityExcludeAccounts)
	})

	t.Run("exclude-patterns compile from a comma-separated flag", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load([]string{
			"--activity-exclude-patterns", `^Uploaded patch set, ^Build (started|running)`,
		}, env(secrets()))
		require.NoError(t, err)

		patterns := cfg.ActivityExcludePatterns
		require.Len(t, patterns, 2)
		assert.True(t, patterns[0].MatchString("Uploaded patch set 4"))
		assert.True(t, patterns[1].MatchString("Build running: 12 files"))
		assert.False(t, patterns[1].MatchString("Build finished"))
	})

	t.Run("exclude-patterns resolve from env mirror", func(t *testing.T) {
		t.Parallel()

		m := secrets()

		m["GERRIT_MCP_ACTIVITY_EXCLUDE_PATTERNS"] = "^Uploaded patch set"

		cfg, err := config.Load(nil, env(m))
		require.NoError(t, err)

		require.Len(t, cfg.ActivityExcludePatterns, 1)
		assert.Equal(t, "^Uploaded patch set", cfg.ActivityExcludePatterns[0].String())
	})

	t.Run("invalid pattern fails startup naming it", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{"--activity-exclude-patterns", "[unclosed"}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid exclude pattern")
		assert.ErrorContains(t, err, "[unclosed")
	})

	t.Run("every invalid pattern named alongside other errors", func(t *testing.T) {
		t.Parallel()

		_, err := config.Load([]string{
			"--activity-exclude-patterns", "[one,ok.*,(two", "--groups", "bogus",
		}, env(secrets()))
		require.Error(t, err)
		assert.ErrorContains(t, err, "[one")
		assert.ErrorContains(t, err, "(two")
		assert.ErrorContains(t, err, "unknown capability groups")
	})
}

func Test_Load_Connection(t *testing.T) {
	t.Parallel()

	t.Run("variables resolve from env", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(nil, env(secrets()))
		require.NoError(t, err)

		assert.Equal(t, "https://gerrit.example.com", cfg.GerritURL)
		assert.Equal(t, "bot", cfg.Username)
		assert.Equal(t, "s3cret", cfg.Token)
		assert.Empty(t, cfg.MissingEnv())
	})

	t.Run("unset variables are named, not an error", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(nil, env(map[string]string{}))
		require.NoError(t, err)
		assert.Equal(t, []string{"GERRIT_URL", "GERRIT_USERNAME", "GERRIT_TOKEN"}, cfg.MissingEnv())
	})

	t.Run("one unset variable named alone", func(t *testing.T) {
		t.Parallel()

		m := secrets()
		delete(m, "GERRIT_TOKEN")

		cfg, err := config.Load(nil, env(m))
		require.NoError(t, err)
		assert.Equal(t, []string{"GERRIT_TOKEN"}, cfg.MissingEnv())
	})
}
