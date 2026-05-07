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
