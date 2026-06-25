// Command prmigrate migrates Bitbucket Cloud pull requests into GitHub repositories.
//
// Usage:
//
//	prmigrate -config config.toml -repo workspace/repo
//	prmigrate -config config.toml -repo workspace/repo -gh-repo org/repo
//	prmigrate -config config.toml -repo workspace/repo -dry-run
//	prmigrate -config config.toml -all
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/pipeline"
)

func main() {
	var (
		configPath = flag.String("config", "config.toml", "path to YAML config file")
		repo       = flag.String("repo", "", `Bitbucket repo to migrate, e.g. "workspace/myrepo"`)
		ghRepo     = flag.String("gh-repo", "", `GitHub repo to migrate into, e.g. "org/repo" (overrides repo_mapping when used with -repo)`)
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
	if *dryRun {
		cfg.Tuning.DryRun = true
	}
	if err := cfg.ResolveSecrets(); err != nil && !cfg.Tuning.DryRun {
		fail(log, "github auth", err)
	}
	if cfg.Bitbucket.Token == "" {
		fail(log, "bitbucket auth", fmt.Errorf("set bitbucket.token or PRMIGRATE_BITBUCKET_TOKEN"))
	}

	// Validate flag combinations.
	if *ghRepo != "" && *repo == "" {
		fail(log, "flag validation", fmt.Errorf("-gh-repo requires -repo"))
	}
	if *ghRepo != "" && *all {
		fail(log, "flag validation", fmt.Errorf("-gh-repo cannot be used with -all"))
	}

	// Validate flag formats.
	if *repo != "" && !strings.Contains(*repo, "/") {
		fail(log, "flag validation", fmt.Errorf("-repo must be in workspace/repo form, got %q", *repo))
	}
	if *ghRepo != "" && !strings.Contains(*ghRepo, "/") {
		fail(log, "flag validation", fmt.Errorf("-gh-repo must be in org/repo form, got %q", *ghRepo))
	}

	// Decide target set.
	var targets [][2]string // {bb, gh} pairs
	switch {
	case *all:
		for bb, gh := range cfg.RepoMapping {
			targets = append(targets, [2]string{bb, gh})
		}
	case *repo != "" && *ghRepo != "":
		targets = [][2]string{{*repo, *ghRepo}}
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
		if cfg.Tuning.DryRun {
			report := m.DryRunReport()
			printDryRunReport(os.Stdout, bb, gh, report, *verbose)
		}
	}
}

func printDryRunReport(w io.Writer, bbRepo, ghRepo string, report pipeline.DryRunReport, verbose bool) {
	const divider = "────────────────────────────────────────────────────────────────────────"

	if verbose {
		for _, e := range report.Entries {
			switch e.Action {
			case pipeline.ActionGitHubPR:
				fmt.Fprintf(w, "\n── #%d %s [GitHub PR: %s → %s] ──\n", e.PRNumber, e.Title, e.Head, e.Base)
			case pipeline.ActionIssueImport:
				fmt.Fprintf(w, "\n── #%d %s [Issue Import / %s] ──\n", e.PRNumber, e.Title, e.State)
			case pipeline.ActionPlaceholder:
				fmt.Fprintf(w, "\n── #%d %s [Placeholder] ──\n", e.PRNumber, e.Title)
			}
			fmt.Fprintln(w, e.Body)
			fmt.Fprintln(w, divider)
		}
	}

	fmt.Fprintf(w, "\n=== Dry Run: %s → %s ===\n", bbRepo, ghRepo)
	fmt.Fprintf(w, "  GitHub PR (branch exists):       %d\n", report.CountByAction(pipeline.ActionGitHubPR))
	fmt.Fprintf(w, "  Issue Import (merged/fallback):  %d\n", report.CountByAction(pipeline.ActionIssueImport))
	fmt.Fprintf(w, "  Placeholder (gap fill):          %d\n", report.CountByAction(pipeline.ActionPlaceholder))
	fmt.Fprintln(w, "  "+strings.Repeat("─", 33))
	fmt.Fprintf(w, "  Total:                           %d\n", report.Total())
}

func fail(log *slog.Logger, op string, err error) {
	log.Error(op, "err", err)
	os.Exit(1)
}
