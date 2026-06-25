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

func TestResolveSecrets_githubToken_fromTOML(t *testing.T) {
	cfg := &config.Config{
		Bitbucket: config.BitbucketConfig{Token: "bb-tok"},
		GitHub:    config.GitHubConfig{APIBase: "https://api.github.com", Token: "toml-tok"},
	}
	if err := cfg.ResolveSecrets(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.Token != "toml-tok" {
		t.Errorf("expected TOML token preserved, got %q", cfg.GitHub.Token)
	}
}

func TestResolveSecrets_githubToken_fromEnvVar(t *testing.T) {
	t.Setenv("PRMIGRATE_GITHUB_TOKEN", "env-tok")
	cfg := &config.Config{
		Bitbucket: config.BitbucketConfig{Token: "bb-tok"},
		GitHub:    config.GitHubConfig{APIBase: "https://api.github.com"},
	}
	if err := cfg.ResolveSecrets(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.Token != "env-tok" {
		t.Errorf("expected env var token, got %q", cfg.GitHub.Token)
	}
}

func TestResolveSecrets_githubToken_fromGhCLI(t *testing.T) {
	// GH_TOKEN は cli/go-gh の auth.TokenForHost が読む環境変数
	t.Setenv("GH_TOKEN", "gh-cli-tok")
	cfg := &config.Config{
		Bitbucket: config.BitbucketConfig{Token: "bb-tok"},
		GitHub:    config.GitHubConfig{APIBase: "https://api.github.com"},
	}
	if err := cfg.ResolveSecrets(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.Token != "gh-cli-tok" {
		t.Errorf("expected gh CLI token, got %q", cfg.GitHub.Token)
	}
}

func TestResolveSecrets_githubToken_notFound_returnsError(t *testing.T) {
	// すべての手段でトークンが得られない状態を作る
	// GH_TOKEN / GITHUB_TOKEN が未設定で TOML も空の場合
	// GH_CONFIG_DIR に存在しないパスを設定して ~/.config/gh/hosts.yml を無効化する
	// GH_PATH に存在しないパスを設定して gh コマンドへのフォールバックを無効化する
	// （cli/go-gh の config.Read は sync.Once でキャッシュされるため、
	//   GH_CONFIG_DIR だけでは別テストのキャッシュが残る可能性がある）
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_CONFIG_DIR", t.TempDir()+"/nonexistent-gh-config")
	t.Setenv("GH_PATH", "/nonexistent/gh")
	cfg := &config.Config{
		Bitbucket: config.BitbucketConfig{Token: "bb-tok"},
		GitHub:    config.GitHubConfig{APIBase: "https://api.github.com"},
	}
	err := cfg.ResolveSecrets()
	if err == nil {
		t.Fatal("expected error when no github token available, got nil")
	}
	want := "github token not found: set github.token in config, PRMIGRATE_GITHUB_TOKEN env var, or run \"gh auth login\""
	if err.Error() != want {
		t.Errorf("error message mismatch\ngot:  %q\nwant: %q", err.Error(), want)
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
