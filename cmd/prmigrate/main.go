// Command prmigrate migrates Bitbucket Cloud pull requests into GitHub repositories.
//
// Usage:
//
//	prmigrate -config config.toml -repo workspace/repo
//	prmigrate -config config.toml -repo workspace/repo -dry-run
//	prmigrate -config config.toml -all
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/pipeline"
)

func main() {
	var (
		configPath = flag.String("config", "config.toml", "path to YAML config file")
		repo       = flag.String("repo", "", `Bitbucket repo to migrate, e.g. "workspace/myrepo"`)
		all        = flag.Bool("all", false, "migrate every repo in repo_mapping")
		dryRun     = flag.Bool("dry-run", false, "do not write to GitHub; only fetch and transform")
		verbose    = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		fail(log, "load config", err)
	}
	cfg.ResolveSecrets()
	if *dryRun {
		cfg.Tuning.DryRun = true
	}

	if cfg.Bitbucket.Token == "" {
		fail(log, "bitbucket auth", fmt.Errorf("set bitbucket.token or PRMIGRATE_BITBUCKET_TOKEN"))
	}
	if !cfg.Tuning.DryRun && cfg.GitHub.Token == "" {
		fail(log, "github auth", fmt.Errorf("set github.token or PRMIGRATE_GITHUB_TOKEN (or use -dry-run)"))
	}

	// Decide target set.
	var targets [][2]string // {bb, gh} pairs
	switch {
	case *all:
		for bb, gh := range cfg.RepoMapping {
			targets = append(targets, [2]string{bb, gh})
		}
	case *repo != "":
		gh, ok := cfg.LookupRepo(*repo)
		if !ok {
			fail(log, "repo lookup", fmt.Errorf("%q is not in repo_mapping", *repo))
		}
		targets = [][2]string{{*repo, gh}}
	default:
		fmt.Fprintln(os.Stderr, "either -repo or -all is required")
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	for _, pair := range targets {
		bb, gh := pair[0], pair[1]
		log.Info("starting repo migration", "bb", bb, "gh", gh)
		m := pipeline.New(cfg, bb, gh, log)
		if err := m.Run(ctx); err != nil {
			log.Error("repo migration failed", "bb", bb, "gh", gh, "err", err)
		}
	}
}

func fail(log *slog.Logger, op string, err error) {
	log.Error(op, "err", err)
	os.Exit(1)
}
