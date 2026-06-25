package config_test

import (
	"testing"

	"github.com/gozuk16/prmigrate/internal/config"
)

func TestApplyDefaults_BitbucketAPIBase_defaultSet(t *testing.T) {
	cfg := &config.Config{}
	cfg.ApplyDefaults()
	if cfg.Bitbucket.APIBase != "https://api.bitbucket.org/2.0" {
		t.Errorf("expected default 'https://api.bitbucket.org/2.0', got %q", cfg.Bitbucket.APIBase)
	}
}

func TestApplyDefaults_BitbucketAPIBase_customPreserved(t *testing.T) {
	cfg := &config.Config{
		Bitbucket: config.BitbucketConfig{APIBase: "http://mock"},
	}
	cfg.ApplyDefaults()
	if cfg.Bitbucket.APIBase != "http://mock" {
		t.Errorf("expected custom value preserved, got %q", cfg.Bitbucket.APIBase)
	}
}

func TestValidate_emptyRepoMapping_ok(t *testing.T) {
	cfg := &config.Config{
		Bitbucket:   config.BitbucketConfig{Username: "user"},
		RepoMapping: map[string]string{},
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error with empty repo_mapping, got: %v", err)
	}
}

func TestGithubHostname(t *testing.T) {
	tests := []struct {
		apiBase string
		want    string
	}{
		{"https://api.github.com", "github.com"},
		{"https://api.github.com/", "github.com"},
		{"https://github.example.com/api/v3", "github.example.com"},
		{"https://github.example.com", "github.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.apiBase, func(t *testing.T) {
			got := config.GithubHostname(tt.apiBase)
			if got != tt.want {
				t.Errorf("GithubHostname(%q) = %q, want %q", tt.apiBase, got, tt.want)
			}
		})
	}
}
