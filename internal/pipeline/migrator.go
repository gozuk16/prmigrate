// Package pipeline orchestrates the migration of one Bitbucket repository's
// pull requests into a target GitHub repository. The high-level flow is:
//
//  1. Enumerate all PR IDs in Bitbucket (sorted ascending).
//  2. For each PR id, in numerical order:
//     a. Fetch the PR detail, comments, and activity stream.
//     b. Transform into a GitHub Issue Import API request.
//     c. Submit, wait for terminal status (so issue numbers are deterministic).
//     d. If the assigned issue number does not match the Bitbucket PR number,
//        warn loudly -- the gap-fill below should prevent this normally.
//  3. Before each real PR, if there is a numerical gap (e.g. PR #5 is missing),
//     submit a dummy "Deleted PR #N" issue so numbering stays aligned.
//
// We submit serially because the GitHub Issue Import API assigns numbers in
// completion order, not submission order; submitting in parallel risks
// re-ordering and is also gentle on the rate limiter.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/githubapi"
	"github.com/gozuk16/prmigrate/internal/githubimport"
	"github.com/gozuk16/prmigrate/internal/transform"
)

// Migrator runs the migration for a single (bitbucket repo, github repo) pair.
type Migrator struct {
	Cfg           *config.Config
	BitbucketRepo string
	GitHubRepo    string

	bb     bitbucket.Fetcher
	gh     *githubimport.Client
	ghapi  *githubapi.Client
	xfmr   *transform.Transformer
	log    *slog.Logger
	report DryRunReport
}

// New constructs a Migrator wired up with all the per-repo clients.
func New(cfg *config.Config, bb bitbucket.Fetcher, bbRepo, ghRepo string, log *slog.Logger) *Migrator {
	return &Migrator{
		Cfg:           cfg,
		BitbucketRepo: bbRepo,
		GitHubRepo:    ghRepo,
		bb:            bb,
		gh:            githubimport.NewClient(cfg.GitHub.APIBase, ghRepo, cfg.GitHub.Token),
		ghapi:         githubapi.NewClient(cfg.GitHub.APIBase, ghRepo, cfg.GitHub.Token),
		xfmr:          transform.New(cfg, bbRepo, ghRepo),
		log:           log.With("bb_repo", bbRepo, "gh_repo", ghRepo),
	}
}

// Run executes the migration end-to-end.
func (m *Migrator) Run(ctx context.Context) error {
	m.log.Info("listing pull requests")
	ids, err := m.bb.ListPullRequestIDs(ctx)
	if err != nil {
		return fmt.Errorf("list PRs: %w", err)
	}
	m.log.Info("found pull requests", "count", len(ids))

	if len(ids) == 0 {
		return nil
	}

	// We expect ids to be the contiguous range 1..max in most repos, but
	// deletions or admin actions can leave gaps. We sort and walk explicitly
	// to handle gaps via placeholder issues.
	sortInts(ids)
	maxID := ids[len(ids)-1]
	idsSet := make(map[int]bool, len(ids))
	for _, id := range ids {
		idsSet[id] = true
	}

	for n := 1; n <= maxID; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		exists, err := m.ghapi.IssueExists(ctx, n)
		if err != nil {
			m.log.Warn("issue existence check failed, proceeding", "n", n, "err", err)
		} else if exists {
			m.log.Info("skipping: already exists on GitHub", "n", n)
			continue
		}

		if !idsSet[n] {
			if !m.Cfg.Tuning.FillGaps {
				continue
			}
			if err := m.submitPlaceholder(ctx, n); err != nil {
				return fmt.Errorf("placeholder #%d: %w", n, err)
			}
			continue
		}

		if err := m.migrateOne(ctx, n); err != nil {
			// Don't abort the whole run on a single PR failure; log and continue.
			m.log.Error("PR migration failed", "pr", n, "err", err)
		}
	}
	return nil
}

func (m *Migrator) migrateOne(ctx context.Context, prID int) error {
	m.log.Info("fetching PR", "pr", prID)
	pr, err := m.bb.GetPullRequest(ctx, prID)
	if err != nil {
		return fmt.Errorf("fetch PR: %w", err)
	}
	comments, err := m.bb.ListComments(ctx, prID)
	if err != nil {
		return fmt.Errorf("fetch comments: %w", err)
	}
	activity, err := m.bb.ListActivity(ctx, prID)
	if err != nil {
		return fmt.Errorf("fetch activity: %w", err)
	}

	// Log routing criteria before deciding GitHub PR vs Issue Import.
	srcBranch, dstBranch := "", ""
	if pr.Source.Branch != nil {
		srcBranch = pr.Source.Branch.Name
	}
	if pr.Destination.Branch != nil {
		dstBranch = pr.Destination.Branch.Name
	}
	m.log.Info("PR route check",
		"pr", prID,
		"state", pr.State,
		"src_branch", srcBranch,
		"dst_branch", dstBranch)

	// For OPEN PRs with a living source branch, attempt GitHub PR API.
	if isOpen(pr.State) && pr.Source.Branch != nil && pr.Destination.Branch != nil {
		created, err := m.tryCreateGitHubPR(ctx, pr, comments, activity)
		if err != nil {
			m.log.Warn("GitHub PR creation failed, falling back to Issue Import",
				"pr", prID, "err", err)
		} else if created {
			return nil
		}
	} else {
		m.log.Info("skipping GitHub PR attempt",
			"pr", prID,
			"not_open", !isOpen(pr.State),
			"no_src_branch", pr.Source.Branch == nil,
			"no_dst_branch", pr.Destination.Branch == nil)
	}

	req := m.xfmr.PullRequestToImport(pr, comments, activity)

	if m.Cfg.Tuning.DryRun {
		m.log.Info("dry-run: would import PR", "pr", prID,
			"title", pr.Title,
			"comments", len(req.Comments),
			"body_bytes", len(req.Issue.Body))
		m.report.Entries = append(m.report.Entries, DryRunEntry{
			PRNumber:     prID,
			Title:        pr.Title,
			Action:       ActionIssueImport,
			State:        pr.State,
			CommentCount: len(req.Comments),
			Body:         req.Issue.Body,
		})
		return nil
	}

	status, err := m.gh.SubmitAndWait(ctx, req)
	if err != nil {
		return fmt.Errorf("submit import: %w", err)
	}
	if status.Status != "imported" {
		return fmt.Errorf("import non-imported status=%s errors=%v", status.Status, status.Errors)
	}
	assigned := status.IssueNumber()
	if assigned != prID {
		m.log.Warn("issue number mismatch",
			"bitbucket_pr", prID, "github_issue", assigned)
	} else {
		m.log.Info("imported", "pr", prID, "issue", assigned)
	}
	return nil
}

// tryCreateGitHubPR attempts to create a real GitHub PR for an OPEN Bitbucket PR.
// Returns (true, nil) on success, (false, nil) if the source branch is absent
// (caller should fall back to Issue Import), or (false, err) on unexpected failure.
func (m *Migrator) tryCreateGitHubPR(
	ctx context.Context,
	pr *bitbucket.PullRequest,
	comments []bitbucket.Comment,
	activity []bitbucket.Activity,
) (bool, error) {
	m.log.Info("checking GitHub branch", "pr", pr.ID, "branch", pr.Source.Branch.Name)
	exists, err := m.ghapi.BranchExists(ctx, pr.Source.Branch.Name)
	if err != nil {
		return false, fmt.Errorf("branch check: %w", err)
	}
	m.log.Info("GitHub branch check result", "pr", pr.ID, "branch", pr.Source.Branch.Name, "exists", exists)
	if !exists {
		m.log.Info("source branch deleted; will import as Issue",
			"pr", pr.ID, "branch", pr.Source.Branch.Name)
		return false, nil
	}

	if m.Cfg.Tuning.DryRun {
		body := m.xfmr.BuildPRBody(pr)
		m.log.Info("dry-run: would create GitHub PR",
			"pr", pr.ID, "head", pr.Source.Branch.Name, "base", pr.Destination.Branch.Name)
		m.report.Entries = append(m.report.Entries, DryRunEntry{
			PRNumber: pr.ID,
			Title:    pr.Title,
			Action:   ActionGitHubPR,
			State:    pr.State,
			Head:     pr.Source.Branch.Name,
			Base:     pr.Destination.Branch.Name,
			Body:     body,
		})
		return true, nil
	}

	body := m.xfmr.BuildPRBody(pr)
	ghPR, err := m.ghapi.CreatePullRequest(ctx, &githubapi.CreatePullRequestRequest{
		Title: pr.Title,
		Body:  body,
		Head:  pr.Source.Branch.Name,
		Base:  pr.Destination.Branch.Name,
	})
	if err != nil {
		return false, fmt.Errorf("create PR: %w", err)
	}

	m.log.Info("created GitHub PR", "bb_pr", pr.ID, "gh_pr", ghPR.Number, "url", ghPR.HTMLURL)
	if ghPR.Number != pr.ID {
		m.log.Warn("PR number mismatch", "bitbucket_pr", pr.ID, "github_pr", ghPR.Number)
	}

	for _, commentBody := range m.xfmr.BuildCommentBodies(comments, activity) {
		if err := m.ghapi.CreateIssueComment(ctx, ghPR.Number, commentBody); err != nil {
			m.log.Warn("failed to post comment on PR", "gh_pr", ghPR.Number, "err", err)
		}
	}

	return true, nil
}

func isOpen(state string) bool {
	return state == "OPEN"
}

func (m *Migrator) submitPlaceholder(ctx context.Context, n int) error {
	now := time.Now().UTC()
	req := &githubimport.ImportRequest{
		Issue: githubimport.Issue{
			Title:     fmt.Sprintf("Deleted Bitbucket PR #%d", n),
			Body:      "_This Bitbucket pull request number was missing or deleted at migration time._",
			CreatedAt: &now,
			UpdatedAt: &now,
			ClosedAt:  &now,
			Closed:    true,
			Labels:    []string{"placeholder"},
		},
	}
	if m.Cfg.Tuning.DryRun {
		m.log.Info("dry-run: would create placeholder", "n", n)
		m.report.Entries = append(m.report.Entries, DryRunEntry{
			PRNumber: n,
			Title:    fmt.Sprintf("Deleted Bitbucket PR #%d", n),
			Action:   ActionPlaceholder,
			Body:     "_This Bitbucket pull request number was missing or deleted at migration time._",
		})
		return nil
	}
	status, err := m.gh.SubmitAndWait(ctx, req)
	if err != nil {
		return err
	}
	if status.Status != "imported" {
		return fmt.Errorf("placeholder import status=%s errors=%v", status.Status, status.Errors)
	}
	m.log.Info("placeholder created", "n", n, "issue", status.IssueNumber())
	return nil
}

func sortInts(a []int) {
	// stdlib generics sort, kept inline to avoid importing slices for one call site.
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// DryRunReport returns the collected dry-run entries after Run() completes.
// Returns an empty report if DryRun was not enabled.
func (m *Migrator) DryRunReport() DryRunReport {
	return m.report
}
