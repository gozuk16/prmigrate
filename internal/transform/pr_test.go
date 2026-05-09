package transform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/transform"
)

func newTestTransformer() *transform.Transformer {
	cfg := &config.Config{
		UserMapping: map[string]string{"alice": "gh-alice"},
		RepoMapping: map[string]string{"ws/repo": "org/repo"},
	}
	cfg.ApplyDefaults()
	return transform.New(cfg, "ws/repo", "org/repo")
}

func makeOpenPR() *bitbucket.PullRequest {
	created := time.Date(2024, 1, 10, 9, 0, 0, 0, time.UTC)
	return &bitbucket.PullRequest{
		ID:          3,
		Title:       "Add feature",
		Description: "This adds a new feature.",
		State:       "OPEN",
		CreatedOn:   created,
		UpdatedOn:   created,
		Author:      &bitbucket.User{Nickname: "alice", DisplayName: "Alice"},
		Source:      bitbucket.Endpoint{Branch: &bitbucket.Branch{Name: "feature/add"}},
		Destination: bitbucket.Endpoint{Branch: &bitbucket.Branch{Name: "main"}},
	}
}

func TestBuildPRBody_containsMetadata(t *testing.T) {
	xfmr := newTestTransformer()
	pr := makeOpenPR()

	body := xfmr.BuildPRBody(pr)

	checks := []string{
		"Pull request",
		"@gh-alice",
		"#3",
		"OPEN",
		"feature/add",
		"main",
		"This adds a new feature.",
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("BuildPRBody: expected body to contain %q\nbody:\n%s", want, body)
		}
	}
}

func TestBuildCommentBodies_returnsInOrder(t *testing.T) {
	xfmr := newTestTransformer()

	t1 := time.Date(2024, 1, 11, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 11, 11, 0, 0, 0, time.UTC)

	comments := []bitbucket.Comment{
		{
			ID:        1,
			CreatedOn: t2,
			UpdatedOn: t2,
			Content:   bitbucket.Content{Raw: "Second comment"},
			User:      &bitbucket.User{Nickname: "alice", DisplayName: "Alice"},
		},
		{
			ID:        2,
			CreatedOn: t1,
			UpdatedOn: t1,
			Content:   bitbucket.Content{Raw: "First comment"},
			User:      &bitbucket.User{Nickname: "alice", DisplayName: "Alice"},
		},
	}

	bodies := xfmr.BuildCommentBodies(comments, nil)

	if len(bodies) != 2 {
		t.Fatalf("expected 2 comment bodies, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "First comment") {
		t.Errorf("expected first body to contain 'First comment', got: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "Second comment") {
		t.Errorf("expected second body to contain 'Second comment', got: %s", bodies[1])
	}
}

func TestBuildCommentBodies_skipsDeleted(t *testing.T) {
	xfmr := newTestTransformer()

	t1 := time.Date(2024, 1, 11, 10, 0, 0, 0, time.UTC)
	comments := []bitbucket.Comment{
		{
			ID:        1,
			CreatedOn: t1,
			UpdatedOn: t1,
			Content:   bitbucket.Content{Raw: "visible"},
			User:      &bitbucket.User{Nickname: "alice"},
			Deleted:   false,
		},
		{
			ID:        2,
			CreatedOn: t1,
			UpdatedOn: t1,
			Content:   bitbucket.Content{Raw: ""},
			User:      &bitbucket.User{Nickname: "alice"},
			Deleted:   true,
		},
	}

	bodies := xfmr.BuildCommentBodies(comments, nil)
	if len(bodies) != 1 {
		t.Errorf("expected 1 body (deleted comment filtered), got %d", len(bodies))
	}
}

func TestBuildPRBody_dateUsesLocalTimezone(t *testing.T) {
	// Temporarily override time.Local to a known non-UTC zone.
	// This makes the test deterministic regardless of the CI environment.
	origLocal := time.Local
	time.Local = time.FixedZone("TST", 5*3600) // UTC+5, fictional "Test Standard Time"
	defer func() { time.Local = origLocal }()

	xfmr := newTestTransformer()
	pr := makeOpenPR() // CreatedOn = 2024-01-10 09:00 UTC

	body := xfmr.BuildPRBody(pr)

	// 09:00 UTC in UTC+5 = 14:00 TST
	if !strings.Contains(body, "2024-01-10 14:00 TST") {
		t.Errorf("expected date in local timezone (TST), body:\n%s", body)
	}
	if strings.Contains(body, "09:00 UTC") {
		t.Errorf("date should not be hardcoded UTC, body:\n%s", body)
	}
}
