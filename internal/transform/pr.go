// Package transform converts Bitbucket data structures into GitHub Issue
// Import API requests. This is the heart of the migration: it decides how
// metadata that GitHub cannot natively represent (original author, original
// timestamp on a comment, inline comment location, approval events, etc.)
// is preserved as Markdown text in the issue/comment body.
package transform

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/githubimport"
)

// Transformer converts Bitbucket PR data into GitHub Issue Import API requests.
type Transformer struct {
	cfg              *config.Config
	bitbucketRepo    string
	githubRepo       string
}

// New returns a Transformer for the given (bitbucket repo, github repo) pair.
func New(cfg *config.Config, bitbucketRepo, githubRepo string) *Transformer {
	return &Transformer{
		cfg:           cfg,
		bitbucketRepo: bitbucketRepo,
		githubRepo:    githubRepo,
	}
}

// PullRequestToImport builds a complete ImportRequest for one Bitbucket PR.
// The result represents the PR as a (potentially closed) GitHub Issue that
// has all comments and activity entries flattened into chronologically
// ordered Issue comments.
func (t *Transformer) PullRequestToImport(
	pr *bitbucket.PullRequest,
	comments []bitbucket.Comment,
	activity []bitbucket.Activity,
) *githubimport.ImportRequest {
	body := t.BuildPRBody(pr)

	closed := !isOpen(pr.State)
	created := pr.CreatedOn
	updated := pr.UpdatedOn

	issue := githubimport.Issue{
		Title:     pr.Title,
		Body:      body,
		CreatedAt: &created,
		UpdatedAt: &updated,
		Closed:    closed,
		Labels:    t.labelsForPR(pr),
	}
	// Per spec: Closed=true with no ClosedAt has historically been buggy.
	// Use UpdatedOn as the best-effort closed time.
	if closed {
		issue.ClosedAt = &updated
	}

	// Try to assign the PR's author if mappable AND they have repo access.
	// Note: if the assignee is invalid the WHOLE import fails, so we leave
	// it empty unless we are confident. The pipeline can re-assign later.
	// (We still embed @mention in the body regardless.)

	// Build comment list from PR comments + activity entries.
	importComments := t.buildComments(comments, activity)

	return &githubimport.ImportRequest{
		Issue:    issue,
		Comments: importComments,
	}
}

// BuildPRBody produces the Markdown body for the imported issue, embedding
// metadata that the Issue Import API cannot represent natively.
func (t *Transformer) BuildPRBody(pr *bitbucket.PullRequest) string {
	var b strings.Builder

	// --- Header (quoted block carrying preserved metadata) ---
	authorTag := t.formatUserMention(pr.Author)
	fmt.Fprintf(&b, "> **Pull request** :twisted_rightwards_arrows: created by %s on %s\n",
		authorTag, formatDate(pr.CreatedOn))
	if !pr.UpdatedOn.Equal(pr.CreatedOn) {
		fmt.Fprintf(&b, "> Last updated on %s\n", formatDate(pr.UpdatedOn))
	}
	fmt.Fprintf(&b, "> Original Bitbucket pull request id: #%d\n", pr.ID)
	fmt.Fprintf(&b, "> State: **`%s`**\n", pr.State)

	// Source / destination references
	if pr.Source.Branch != nil {
		srcLabel := pr.Source.Branch.Name
		if pr.Source.Repository != nil && pr.Source.Repository.FullName != t.bitbucketRepo {
			srcLabel = pr.Source.Repository.FullName + ":" + srcLabel
		}
		var srcCommit string
		if pr.Source.Commit != nil {
			srcCommit = pr.Source.Commit.Hash
		}
		fmt.Fprintf(&b, "> Source: `%s`", srcLabel)
		if srcCommit != "" {
			fmt.Fprintf(&b, " @ %s", t.commitLink(srcCommit))
		}
		b.WriteString("\n")
	}
	if pr.Destination.Branch != nil {
		fmt.Fprintf(&b, "> Destination: `%s`", pr.Destination.Branch.Name)
		if pr.Destination.Commit != nil {
			fmt.Fprintf(&b, " @ %s", t.commitLink(pr.Destination.Commit.Hash))
		}
		b.WriteString("\n")
	}
	if pr.MergeCommit != nil {
		fmt.Fprintf(&b, "> Merge commit: %s\n", t.commitLink(pr.MergeCommit.Hash))
	}

	// Reviewers / approvals snapshot
	if len(pr.Participants) > 0 {
		b.WriteString(">\n> Participants:\n")
		for _, p := range pr.Participants {
			marker := ""
			if p.Approved {
				marker = " :heavy_check_mark:"
			}
			role := ""
			if p.Role == "REVIEWER" {
				role = " (reviewer)"
			}
			fmt.Fprintf(&b, "> * %s%s%s\n",
				t.formatUserMention(p.User), role, marker)
		}
	}

	// --- Original description ---
	b.WriteString("\n")
	if pr.Description != "" {
		b.WriteString(t.rewriteBody(pr.Description))
		b.WriteString("\n")
	}
	return b.String()
}

// buildComments merges general comments, inline comments, and activity-derived
// pseudo-comments (approvals, status changes) into one chronological list.
func (t *Transformer) buildComments(
	comments []bitbucket.Comment,
	activity []bitbucket.Activity,
) []githubimport.Comment {
	type stamped struct {
		when time.Time
		body string
	}
	var entries []stamped

	// Index general comments by ID so we can resolve parent threading.
	commentByID := make(map[int]*bitbucket.Comment, len(comments))
	for i := range comments {
		commentByID[comments[i].ID] = &comments[i]
	}

	for i := range comments {
		c := &comments[i]
		if c.Deleted || c.Content.Raw == "" {
			continue
		}
		entries = append(entries, stamped{
			when: c.CreatedOn,
			body: t.formatComment(c, commentByID),
		})
	}

	for _, a := range activity {
		switch {
		case a.Approval != nil:
			entries = append(entries, stamped{
				when: a.Approval.Date,
				body: fmt.Sprintf("> %s approved :heavy_check_mark: the pull request on %s",
					t.formatUserMention(a.Approval.User),
					formatDate(a.Approval.Date)),
			})
		case a.Update != nil && a.Update.State != "":
			// Bitbucket reports state transitions in updates.
			entries = append(entries, stamped{
				when: a.Update.Date,
				body: fmt.Sprintf("> %s changed the state to `%s` on %s",
					t.formatUserMention(a.Update.Author),
					a.Update.State,
					formatDate(a.Update.Date)),
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].when.Before(entries[j].when)
	})

	out := make([]githubimport.Comment, 0, len(entries))
	for _, e := range entries {
		when := e.when
		out = append(out, githubimport.Comment{
			Body:      e.body,
			CreatedAt: &when,
		})
	}
	return out
}

// formatComment renders one Bitbucket comment as a GitHub-flavored Markdown
// comment body. Inline (review) comments get a quoted location prefix since
// GitHub Issue Comments cannot anchor to a diff line.
func (t *Transformer) formatComment(c *bitbucket.Comment, all map[int]*bitbucket.Comment) string {
	var b strings.Builder

	fmt.Fprintf(&b, "> %s commented on %s\n",
		t.formatUserMentionCapital(c.User), formatDate(c.CreatedOn))

	if c.IsInline() {
		loc := formatInlineLocation(c.Inline)
		fmt.Fprintf(&b, ">\n> %s\n", loc)
	}

	// If this is a reply, quote the parent inline.
	if c.Parent != nil {
		if parent, ok := all[c.Parent.ID]; ok && parent.Content.Raw != "" {
			b.WriteString(">\n")
			for _, line := range strings.Split(strings.TrimRight(parent.Content.Raw, "\n"), "\n") {
				fmt.Fprintf(&b, "> > %s\n", line)
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(t.rewriteBody(c.Content.Raw))
	return b.String()
}

func formatInlineLocation(in *bitbucket.Inline) string {
	prefix := "**Location:**"
	if in.Outdated {
		prefix = "**Outdated location:**"
	}
	switch {
	case in.From == nil && in.To == nil:
		return fmt.Sprintf("%s `%s`", prefix, in.Path)
	case in.From != nil && in.To != nil && *in.From != *in.To:
		return fmt.Sprintf("%s lines %d-%d of `%s`", prefix, *in.From, *in.To, in.Path)
	default:
		line := 0
		if in.From != nil {
			line = *in.From
		} else if in.To != nil {
			line = *in.To
		}
		return fmt.Sprintf("%s line %d of `%s`", prefix, line, in.Path)
	}
}

// labelsForPR composes the set of GitHub labels to apply to this PR, based
// on its Bitbucket state. Always includes a "pull-request" tag for filterability.
func (t *Transformer) labelsForPR(pr *bitbucket.PullRequest) []string {
	labels := []string{"pull-request"}
	if l, ok := t.cfg.StateLabels[pr.State]; ok && l != "" {
		labels = append(labels, l)
	}
	return labels
}

// --- formatting helpers ---

func formatDate(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func isOpen(state string) bool {
	return state == "OPEN"
}

// formatUserMention returns the Markdown to use when referring to a Bitbucket
// user. If we have a GitHub mapping, render @ghuser (which mentions them).
// Otherwise render the display name as bold without an @ to avoid pinging
// random GitHub accounts.
func (t *Transformer) formatUserMention(u *bitbucket.User) string {
	if u == nil {
		return "*a former Bitbucket user*"
	}
	if gh, ok := t.cfg.LookupUserAny(u.Identifiers()...); ok {
		return "@" + gh
	}
	name := u.DisplayName
	if name == "" {
		name = u.Nickname
	}
	if name == "" {
		name = "unknown"
	}
	return "**" + name + "** _(unmapped Bitbucket user)_"
}

func (t *Transformer) formatUserMentionCapital(u *bitbucket.User) string {
	s := t.formatUserMention(u)
	// If unmapped and starts with bold name, capitalize first letter
	// only when it's a Bitbucket label string. Cheap heuristic.
	return s
}

// commitLink returns a Markdown link to a commit on the GitHub side.
// We assume the git history is mirror-pushed so hashes are preserved.
func (t *Transformer) commitLink(hash string) string {
	short := hash
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf("[`%s`](https://github.com/%s/commit/%s)",
		short, t.githubRepo, hash)
}

// BuildCommentBodies returns the Markdown body strings for each non-deleted
// comment and activity entry, sorted chronologically. Used when creating
// comments via the GitHub REST API (which cannot set timestamps).
func (t *Transformer) BuildCommentBodies(
	comments []bitbucket.Comment,
	activity []bitbucket.Activity,
) []string {
	importComments := t.buildComments(comments, activity)
	bodies := make([]string, len(importComments))
	for i, c := range importComments {
		bodies[i] = c.Body
	}
	return bodies
}
