package transform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
)

// makePRWithDesc creates a PR with the given description and nil Author,
// so the header block does not inject @mentions that could interfere with
// mention-rewrite assertions.
func makePRWithDesc(desc string) *bitbucket.PullRequest {
	t := time.Date(2024, 1, 10, 9, 0, 0, 0, time.UTC)
	return &bitbucket.PullRequest{
		ID: 1, Title: "t", State: "OPEN",
		CreatedOn: t, UpdatedOn: t,
		Description: desc,
	}
}

func TestRewriteBody_pullRequestURL(t *testing.T) {
	pr := makePRWithDesc("See https://bitbucket.org/ws/repo/pull-requests/5")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/pull/5") {
		t.Errorf("pull-request URL not rewritten; body:\n%s", body)
	}
	if strings.Contains(body, "bitbucket.org/ws/repo/pull-requests/5") {
		t.Errorf("original bitbucket URL should be replaced; body:\n%s", body)
	}
}

func TestRewriteBody_issueURL(t *testing.T) {
	pr := makePRWithDesc("Fixes https://bitbucket.org/ws/repo/issues/3")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/issues/3") {
		t.Errorf("issue URL not rewritten; body:\n%s", body)
	}
}

func TestRewriteBody_commitURL(t *testing.T) {
	pr := makePRWithDesc("See https://bitbucket.org/ws/repo/commits/abc123def456789")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/commit/abc123def456789") {
		t.Errorf("commit URL not rewritten; body:\n%s", body)
	}
}

func TestRewriteBody_unmappedRepo(t *testing.T) {
	originalURL := "https://bitbucket.org/other/repo/pull-requests/5"
	pr := makePRWithDesc("See " + originalURL)
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, originalURL) {
		t.Errorf("unmapped repo URL should be left unchanged; body:\n%s", body)
	}
}

func TestRewriteBody_mappedMention(t *testing.T) {
	pr := makePRWithDesc("@alice fixed this xz789")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "@gh-alice fixed this xz789") {
		t.Errorf("mapped mention @alice should become @gh-alice; body:\n%s", body)
	}
}

func TestRewriteBody_unmappedMention(t *testing.T) {
	pr := makePRWithDesc("@unknown frobnicates xz789")
	body := newTestTransformer().BuildPRBody(pr)
	if strings.Contains(body, "@unknown") {
		t.Errorf("unmapped @unknown should have @ stripped; body:\n%s", body)
	}
	if !strings.Contains(body, "unknown frobnicates xz789") {
		t.Errorf("unmapped mention text should be preserved without @; body:\n%s", body)
	}
}

func TestRewriteBody_srcURL(t *testing.T) {
	pr := makePRWithDesc("See https://bitbucket.org/ws/repo/src/main/README.md")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/blob/main/README.md") {
		t.Errorf("src URL not rewritten to blob; body:\n%s", body)
	}
}

func TestRewriteBody_singularPullRequestURL(t *testing.T) {
	pr := makePRWithDesc("See https://bitbucket.org/ws/repo/pull-request/7")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/pull/7") {
		t.Errorf("singular pull-request URL not rewritten; body:\n%s", body)
	}
}

func TestRewriteBody_singularCommitURL(t *testing.T) {
	pr := makePRWithDesc("See https://bitbucket.org/ws/repo/commit/abc123def456789")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/commit/abc123def456789") {
		t.Errorf("singular commit URL not rewritten; body:\n%s", body)
	}
}

func TestRewriteBody_singularIssueURL(t *testing.T) {
	pr := makePRWithDesc("Fixes https://bitbucket.org/ws/repo/issue/3")
	body := newTestTransformer().BuildPRBody(pr)
	if !strings.Contains(body, "https://github.com/org/repo/issues/3") {
		t.Errorf("singular issue URL not rewritten; body:\n%s", body)
	}
}

func TestRewriteBody_uuidMention(t *testing.T) {
	// Bitbucket uses brace-UUID form @{uuid} for legacy users.
	// When unmapped, the @ is stripped and the brace-UUID text is preserved.
	pr := makePRWithDesc("@{123e4567-e89b-12d3-a456-426614174000} commented")
	body := newTestTransformer().BuildPRBody(pr)
	if strings.Contains(body, "@{123e4567") {
		t.Errorf("unmapped UUID mention should have @ stripped; body:\n%s", body)
	}
	if !strings.Contains(body, "{123e4567-e89b-12d3-a456-426614174000} commented") {
		t.Errorf("UUID text should be preserved without @; body:\n%s", body)
	}
}
