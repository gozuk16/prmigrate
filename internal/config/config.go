// Package config defines the on-disk TOML configuration for prmigrate and
// provides loaders + lookup helpers used throughout the migration pipeline.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the root configuration. Loaded from a TOML file specified on the CLI.
type Config struct {
	// Bitbucket workspace + auth.
	Bitbucket BitbucketConfig `toml:"bitbucket"`

	// GitHub API base URL + auth.
	GitHub GitHubConfig `toml:"github"`

	// Mapping of Bitbucket users to GitHub users. The key may be a
	// nickname, UUID (with braces) or Atlassian account_id ("557058:..."),
	// since Bitbucket returns different forms in different responses.
	UserMapping map[string]string `toml:"user_mapping"`

	// Mapping of Bitbucket repositories ("workspace/repo") to GitHub
	// repositories ("org/repo"). Used both to know what to migrate and to
	// rewrite cross-repo links in PR bodies/comments.
	RepoMapping map[string]string `toml:"repo_mapping"`

	// Labels to apply to imported PRs based on Bitbucket state.
	// Defaults are filled in by ApplyDefaults() if missing.
	StateLabels map[string]string `toml:"state_labels"`

	// Behavior tuning.
	Tuning TuningConfig `toml:"tuning"`
}

type BitbucketConfig struct {
	APIBase  string `toml:"api_base"` // default: https://api.bitbucket.org/2.0
	Username string `toml:"username"`
	Token    string `toml:"token"` // app password or API token
}

type GitHubConfig struct {
	APIBase string `toml:"api_base"` // default: https://api.github.com
	Token   string `toml:"token"`
}

type TuningConfig struct {
	// Bitbucket request rate (requests per second). Default 0.25 (≈900/hour).
	BitbucketRPS float64 `toml:"bitbucket_rps"`

	// Whether to create a placeholder issue for missing PR numbers so that
	// numbering aligns. Default true.
	FillGaps bool `toml:"fill_gaps"`

	// If true, do not actually call GitHub APIs; only fetch + transform.
	DryRun bool `toml:"dry_run"`
}

// Load reads the config from path and applies defaults.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ApplyDefaults fills in any unset fields with sensible defaults.
func (c *Config) ApplyDefaults() {
	if c.Bitbucket.APIBase == "" {
		c.Bitbucket.APIBase = "https://api.bitbucket.org/2.0"
	}
	if c.GitHub.APIBase == "" {
		c.GitHub.APIBase = "https://api.github.com"
	}
	if c.Tuning.BitbucketRPS == 0 {
		c.Tuning.BitbucketRPS = 0.25
	}
	if c.StateLabels == nil {
		c.StateLabels = map[string]string{
			"OPEN":       "",          // no extra label
			"MERGED":     "merged",
			"DECLINED":   "declined",
			"SUPERSEDED": "superseded",
		}
	}
}

// Validate checks for required fields. Tokens may be supplied via env vars
// (see ResolveSecrets) and so are not strictly required here.
func (c *Config) Validate() error {
	if c.Bitbucket.Username == "" {
		return fmt.Errorf("bitbucket.username is required")
	}
	for bRepo, gRepo := range c.RepoMapping {
		if !strings.Contains(bRepo, "/") || !strings.Contains(gRepo, "/") {
			return fmt.Errorf(`repo_mapping entries must be in "workspace/repo" form: %q -> %q`, bRepo, gRepo)
		}
	}
	return nil
}

// ResolveSecrets reads tokens from environment variables if the YAML field
// is empty. Convention:
//   - bitbucket.token: PRMIGRATE_BITBUCKET_TOKEN
//   - github.token:    PRMIGRATE_GITHUB_TOKEN
func (c *Config) ResolveSecrets() {
	if c.Bitbucket.Token == "" {
		c.Bitbucket.Token = os.Getenv("PRMIGRATE_BITBUCKET_TOKEN")
	}
	if c.GitHub.Token == "" {
		c.GitHub.Token = os.Getenv("PRMIGRATE_GITHUB_TOKEN")
	}
}

// LookupUser returns the GitHub username corresponding to a Bitbucket user
// identifier (nickname, UUID, or account_id). Returns ("", false) if no
// mapping exists. Empty input returns ("", false).
func (c *Config) LookupUser(bbIdentifier string) (string, bool) {
	if bbIdentifier == "" {
		return "", false
	}
	gh, ok := c.UserMapping[bbIdentifier]
	return gh, ok
}

// LookupUserAny returns the first mapping found for any of the given
// identifiers (typically nickname/UUID/accountID for the same Bitbucket user).
func (c *Config) LookupUserAny(identifiers ...string) (string, bool) {
	for _, id := range identifiers {
		if gh, ok := c.LookupUser(id); ok {
			return gh, true
		}
	}
	return "", false
}

// LookupRepo returns the GitHub repo full name for a Bitbucket repo full name.
func (c *Config) LookupRepo(bbFullName string) (string, bool) {
	gh, ok := c.RepoMapping[bbFullName]
	return gh, ok
}
