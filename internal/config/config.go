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
	"strconv"
	"strings"

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

// Env variable names for connection identity and credentials.
const (
	EnvURL      = "GERRIT_URL"
	EnvUsername = "GERRIT_USERNAME"
	EnvToken    = "GERRIT_TOKEN"
)

var (
	errEnvMissing    = e.New("required environment variable is not set")
	errUnknownGroups = e.New("unknown capability groups")
	errNoGroups      = e.New("no capability groups enabled")
	errParseFlags    = e.New("parse flags")
	errInvalidBool   = e.New("invalid boolean flag value")
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

	err := resolveFlags(args, getenv, []*behaviorFlag{&groups, &includeTools, &excludeTools, &projects, &ownChanges})
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		GerritURL:           getenv(EnvURL),
		Username:            getenv(EnvUsername),
		Token:               getenv(EnvToken),
		Groups:              nil,
		IncludeTools:        splitList(includeTools.value),
		ExcludeTools:        splitList(excludeTools.value),
		Projects:            splitList(projects.value),
		AllowForeignChanges: false,
	}

	errs := missingEnv(cfg)

	ownOnly, err := parseBool(ownChanges)
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.AllowForeignChanges = !ownOnly
	}

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
