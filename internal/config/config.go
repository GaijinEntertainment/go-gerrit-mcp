// Package config resolves the server configuration from CLI flags and
// environment variables. Connection identity and credentials come from
// environment only (GERRIT_URL, GERRIT_USERNAME, GERRIT_TOKEN); behavior
// comes from flags, each mirrored by a GERRIT_MCP_* variable with the flag
// winning when both are set. Zero behavior configuration yields the read
// capability group only.
package config

import (
	"errors"
	"flag"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dev.gaijin.team/go/golib/e"
	"dev.gaijin.team/go/golib/fields"
)

// Group is a capability group name: an independent, self-sufficient unit of
// server capability selected via --groups.
type Group string

// Capability groups. Each write-capable group bundles the minimal change-read
// subset it needs to function on its own; enabled groups union.
const (
	GroupRead       Group = "read"
	GroupComment    Group = "comment"
	GroupTransition Group = "transition"
)

const defaultGroups = "read"

const defaultPollInterval = "60s"

// Env variable names for connection identity and credentials.
const (
	EnvURL      = "GERRIT_URL"
	EnvUsername = "GERRIT_USERNAME"
	EnvToken    = "GERRIT_TOKEN"
)

var (
	errEnvMissing          = e.New("required environment variable is not set")
	errUnknownGroups       = e.New("unknown capability groups")
	errNoGroups            = e.New("no capability groups enabled")
	errParseFlags          = e.New("parse flags")
	errInvalidBool         = e.New("invalid boolean flag value")
	errInvalidDuration     = e.New("invalid duration flag value")
	errIntervalNotPositive = e.New("poll interval must be positive")
	errInvalidPattern      = e.New("invalid exclude pattern")
)

// Config is the resolved server configuration.
type Config struct {
	// GerritURL is the base URL of the Gerrit instance.
	GerritURL string
	// Username authenticates HTTP Basic requests.
	Username string
	// Token is the HTTP credential paired with Username.
	Token string
	// Groups are the enabled capability groups, deduplicated, in input order.
	Groups []Group
	// IncludeTools, when non-empty, keeps only the listed tools from the
	// group-resolved set. It never activates tools outside enabled groups.
	IncludeTools []string
	// ExcludeTools removes the listed tools from the group-resolved set.
	ExcludeTools []string
	// Projects, when non-empty, confines every operation to the listed
	// Gerrit projects.
	Projects []string
	// AllowForeignChanges disables the own-changes restriction: when false
	// (the default), trail-leaving operations are refused on changes not
	// owned by the authenticated account.
	AllowForeignChanges bool
	// ReviewNotifications enables the review-notifications feature: polling
	// Gerrit for activity on subscribed changes and pushing it into the
	// agent's session. Off by default.
	ReviewNotifications bool
	// ReviewNotificationsPollInterval is the cadence of the review
	// notifications poller. Always resolved and validated, even when the
	// feature is disabled.
	ReviewNotificationsPollInterval time.Duration
	// ReviewNotificationsIncludeOwn keeps the authenticated account's own
	// activity in review notifications; by default own activity never
	// becomes a notification.
	ReviewNotificationsIncludeOwn bool
	// ReviewNotificationsExcludeAccounts lists accounts (usernames or
	// numeric account IDs) whose activity never becomes a review
	// notification.
	ReviewNotificationsExcludeAccounts []string
	// ReviewNotificationsExcludePatterns drops review-notification events
	// whose message or comment text matches any pattern. Compiled at load;
	// an invalid pattern fails startup.
	ReviewNotificationsExcludePatterns []*regexp.Regexp
}

// behaviorFlag is one CLI flag with its GERRIT_MCP_* env mirror. The flag
// value wins over the mirror; the mirror wins over the default. New behavior
// options (tool filters, project scoping, own-changes) register here.
type behaviorFlag struct {
	name     string
	mirror   string
	usage    string
	fallback string

	value string `exhaustruct:"optional"`
}

// Load resolves configuration from CLI arguments and the environment.
// It reports every configuration error at once.
func Load(args []string, getenv func(string) string) (*Config, error) {
	groups := behaviorFlag{
		name:     "groups",
		mirror:   "GERRIT_MCP_GROUPS",
		usage:    "comma-separated capability groups to enable: read, comment, transition",
		fallback: defaultGroups,
	}
	includeTools := behaviorFlag{
		name:     "include-tools",
		mirror:   "GERRIT_MCP_INCLUDE_TOOLS",
		usage:    "comma-separated tool names to keep from the group-resolved set; never activates gated tools",
		fallback: "",
	}
	excludeTools := behaviorFlag{
		name:     "exclude-tools",
		mirror:   "GERRIT_MCP_EXCLUDE_TOOLS",
		usage:    "comma-separated tool names to remove from the group-resolved set",
		fallback: "",
	}
	projects := behaviorFlag{
		name:     "projects",
		mirror:   "GERRIT_MCP_PROJECTS",
		usage:    "comma-separated Gerrit project allowlist confining every operation",
		fallback: "",
	}
	ownChanges := behaviorFlag{
		name:     "own-changes-only",
		mirror:   "GERRIT_MCP_OWN_CHANGES_ONLY",
		usage:    "refuse trail-leaving operations on changes not owned by the authenticated account",
		fallback: "true",
	}
	reviewNotifications := behaviorFlag{
		name:     "review-notifications",
		mirror:   "GERRIT_MCP_REVIEW_NOTIFICATIONS",
		usage:    "poll Gerrit for activity on subscribed changes and push it into the agent's session",
		fallback: "false",
	}
	pollInterval := behaviorFlag{
		name:     "review-notifications-poll-interval",
		mirror:   "GERRIT_MCP_REVIEW_NOTIFICATIONS_POLL_INTERVAL",
		usage:    "cadence of the review notifications poller, as a Go duration (e.g. 60s)",
		fallback: defaultPollInterval,
	}
	includeOwn := behaviorFlag{
		name:     "review-notifications-include-own",
		mirror:   "GERRIT_MCP_REVIEW_NOTIFICATIONS_INCLUDE_OWN",
		usage:    "keep the authenticated account's own activity in review notifications",
		fallback: "false",
	}
	excludeAccounts := behaviorFlag{
		name:     "review-notifications-exclude-accounts",
		mirror:   "GERRIT_MCP_REVIEW_NOTIFICATIONS_EXCLUDE_ACCOUNTS",
		usage:    "comma-separated usernames or numeric account IDs whose activity never becomes a review notification",
		fallback: "",
	}
	excludePatterns := behaviorFlag{
		name:   "review-notifications-exclude-patterns",
		mirror: "GERRIT_MCP_REVIEW_NOTIFICATIONS_EXCLUDE_PATTERNS",
		usage: "comma-separated regular expressions; message or comment text matching any of them never " +
			"becomes a review notification",
		fallback: "",
	}

	err := resolveFlags(args, getenv, []*behaviorFlag{
		&groups, &includeTools, &excludeTools, &projects, &ownChanges,
		&reviewNotifications, &pollInterval, &includeOwn, &excludeAccounts, &excludePatterns,
	})
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		GerritURL:                          getenv(EnvURL),
		Username:                           getenv(EnvUsername),
		Token:                              getenv(EnvToken),
		Groups:                             nil,
		IncludeTools:                       splitList(includeTools.value),
		ExcludeTools:                       splitList(excludeTools.value),
		Projects:                           splitList(projects.value),
		AllowForeignChanges:                false,
		ReviewNotifications:                false,
		ReviewNotificationsPollInterval:    0,
		ReviewNotificationsIncludeOwn:      false,
		ReviewNotificationsExcludeAccounts: splitList(excludeAccounts.value),
		ReviewNotificationsExcludePatterns: nil,
	}

	errs := missingEnv(cfg)

	ownOnly, err := parseBool(ownChanges)
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.AllowForeignChanges = !ownOnly
	}

	notifications, err := parseBool(reviewNotifications)
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.ReviewNotifications = notifications
	}

	interval, err := parsePollInterval(pollInterval)
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.ReviewNotificationsPollInterval = interval
	}

	own, err := parseBool(includeOwn)
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.ReviewNotificationsIncludeOwn = own
	}

	patterns, patternErrs := compilePatterns(excludePatterns)

	cfg.ReviewNotificationsExcludePatterns = patterns

	errs = append(errs, patternErrs...)

	parsed, err := parseGroups(groups.value)
	if err != nil {
		errs = append(errs, err)
	}

	cfg.Groups = parsed

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return cfg, nil
}

// resolveFlags parses args into the given flags, then fills every flag that
// was not set explicitly from its env mirror, falling back to its default.
func resolveFlags(args []string, getenv func(string) string, flags []*behaviorFlag) error {
	fs := flag.NewFlagSet("go-gerrit-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	for _, bf := range flags {
		fs.StringVar(&bf.value, bf.name, "", bf.usage)
	}

	if err := fs.Parse(args); err != nil {
		return errParseFlags.Wrap(err)
	}

	explicit := make(map[string]bool, len(flags))
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	for _, bf := range flags {
		if explicit[bf.name] {
			continue
		}

		bf.value = bf.fallback

		if v := getenv(bf.mirror); v != "" {
			bf.value = v
		}
	}

	return nil
}

func missingEnv(cfg *Config) []error {
	var errs []error

	for _, v := range []struct{ name, value string }{
		{EnvURL, cfg.GerritURL},
		{EnvUsername, cfg.Username},
		{EnvToken, cfg.Token},
	} {
		if v.value == "" {
			errs = append(errs, errEnvMissing.WithField("name", v.name))
		}
	}

	return errs
}

// parseBool parses a resolved boolean flag value, naming the flag and the
// offending value in the error.
func parseBool(bf behaviorFlag) (bool, error) {
	v, err := strconv.ParseBool(strings.TrimSpace(bf.value))
	if err != nil {
		return false, errInvalidBool.WithFields(
			fields.F("flag", bf.name),
			fields.F("value", bf.value),
		)
	}

	return v, nil
}

// parsePollInterval parses a resolved duration flag value and rejects
// non-positive intervals, naming the flag and the offending value in the
// error.
func parsePollInterval(bf behaviorFlag) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(bf.value))
	if err != nil {
		return 0, errInvalidDuration.WithFields(
			fields.F("flag", bf.name),
			fields.F("value", bf.value),
		)
	}

	if d <= 0 {
		return 0, errIntervalNotPositive.WithFields(
			fields.F("flag", bf.name),
			fields.F("value", bf.value),
		)
	}

	return d, nil
}

// compilePatterns compiles a comma-separated pattern list, reporting every
// invalid pattern by name so the operator fixes all of them in one pass.
func compilePatterns(bf behaviorFlag) ([]*regexp.Regexp, []error) {
	var (
		compiled []*regexp.Regexp
		errs     []error
	)

	for _, p := range splitList(bf.value) {
		re, err := regexp.Compile(p)
		if err != nil {
			errs = append(errs, errInvalidPattern.Wrap(err,
				fields.F("flag", bf.name),
				fields.F("pattern", p),
			))

			continue
		}

		compiled = append(compiled, re)
	}

	return compiled, errs
}

// splitList splits a comma-separated list, trimming whitespace and dropping
// empty entries. An empty input yields nil.
func splitList(s string) []string {
	var out []string

	for part := range strings.SplitSeq(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func parseGroups(s string) ([]Group, error) {
	known := map[Group]bool{GroupRead: true, GroupComment: true, GroupTransition: true}

	var (
		groups  []Group
		unknown []string
		seen    = make(map[Group]bool, len(known))
	)

	for part := range strings.SplitSeq(s, ",") {
		name := Group(strings.TrimSpace(part))
		if name == "" {
			continue
		}

		if !known[name] {
			unknown = append(unknown, string(name))

			continue
		}

		if !seen[name] {
			seen[name] = true

			groups = append(groups, name)
		}
	}

	if len(unknown) > 0 {
		return nil, errUnknownGroups.WithFields(
			fields.F("groups", strings.Join(unknown, ",")),
			fields.F("known", "read,comment,transition"),
		)
	}

	if len(groups) == 0 {
		return nil, errNoGroups
	}

	return groups, nil
}
