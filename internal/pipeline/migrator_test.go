package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/pipeline"
)

// newTestMigrator creates a Migrator with test server URLs injected.
// tuning.BitbucketRPS must be >= 1000 to avoid rate-limiter delays in tests.
func newTestMigrator(t *testing.T, bbURL, ghURL string, tuning config.TuningConfig) *pipeline.Migrator {
	t.Helper()
	cfg := &config.Config{
		Bitbucket: config.BitbucketConfig{
			APIBase:  bbURL,
			Username: "user",
			Token:    "token",
		},
		GitHub: config.GitHubConfig{
			APIBase: ghURL,
			Token:   "gh-token",
		},
		UserMapping: map[string]string{},
		RepoMapping: map[string]string{"ws/repo": "org/repo"},
		Tuning:      tuning,
	}
	cfg.ApplyDefaults()
	return pipeline.New(cfg, "ws/repo", "org/repo", slog.Default())
}

// terminalImportResponse is a GitHub Import API response with terminal status,
// so SubmitAndWait returns immediately without polling.
const terminalImportResponse = `{"status":"imported","issue_url":"http://example.com/repos/org/repo/issues/1","url":"http://example.com/status/1"}`

// TestMigrator_mergedPR: a MERGED Bitbucket PR is submitted to Import API
// with closed=true and labels containing "merged".
func TestMigrator_mergedPR(t *testing.T) {
	const prJSON = `{"id":1,"title":"Fix bug","state":"MERGED","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00"}`

	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1}]}`)
		case "/repositories/ws/repo/pullrequests/1":
			fmt.Fprint(w, prJSON)
		case "/repositories/ws/repo/pullrequests/1/comments":
			fmt.Fprint(w, `{"values":[]}`)
		case "/repositories/ws/repo/pullrequests/1/activity":
			fmt.Fprint(w, `{"values":[]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	type importBody struct {
		Issue struct {
			Closed bool     `json:"closed"`
			Labels []string `json:"labels"`
		} `json:"issue"`
	}
	var got importBody

	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IssueExists check (idempotency): not yet migrated → 404
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues" {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, terminalImportResponse)
			return
		}
		t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{BitbucketRPS: 1000})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !got.Issue.Closed {
		t.Error("expected import request closed=true for MERGED PR")
	}
	hasLabel := func(labels []string, want string) bool {
		for _, l := range labels {
			if l == want {
				return true
			}
		}
		return false
	}
	if !hasLabel(got.Issue.Labels, "merged") {
		t.Errorf("expected labels to contain 'merged', got %v", got.Issue.Labels)
	}
	if !hasLabel(got.Issue.Labels, "pull-request") {
		t.Errorf("expected labels to contain 'pull-request', got %v", got.Issue.Labels)
	}

	report := m.DryRunReport()
	if report.Total() != 0 {
		t.Errorf("non-dry-run should have empty report, got %d entries", report.Total())
	}
}

// TestMigrator_openPR_branchExists: an OPEN PR whose source branch exists
// is recreated as a real GitHub PR via the REST API.
func TestMigrator_openPR_branchExists(t *testing.T) {
	const prJSON = `{"id":1,"title":"Add feature","state":"OPEN","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00","source":{"branch":{"name":"feature/add"}},"destination":{"branch":{"name":"main"}}}`

	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1}]}`)
		case "/repositories/ws/repo/pullrequests/1":
			fmt.Fprint(w, prJSON)
		case "/repositories/ws/repo/pullrequests/1/comments":
			fmt.Fprint(w, `{"values":[]}`)
		case "/repositories/ws/repo/pullrequests/1/activity":
			fmt.Fprint(w, `{"values":[]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	type prBody struct {
		Head string `json:"head"`
		Base string `json:"base"`
	}
	var gotPR prBody

	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// IssueExists check (idempotency): not yet migrated → 404
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
			rawBranch := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/branches/")
			gotBranch, _ := url.PathUnescape(rawBranch)
			if gotBranch != "feature/add" {
				t.Errorf("branch check: got %q, want feature/add", gotBranch)
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"name":"feature/add"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/pulls":
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gotPR); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number":1,"html_url":"http://example.com/pull/1"}`)
		default:
			t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{BitbucketRPS: 1000})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gotPR.Head != "feature/add" {
		t.Errorf("expected head=feature/add, got %q", gotPR.Head)
	}
	if gotPR.Base != "main" {
		t.Errorf("expected base=main, got %q", gotPR.Base)
	}
}

// TestMigrator_openPR_branchDeleted: an OPEN PR whose source branch is gone (404)
// falls back to the Issue Import API.
func TestMigrator_openPR_branchDeleted(t *testing.T) {
	const prJSON = `{"id":1,"title":"Del feature","state":"OPEN","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00","source":{"branch":{"name":"feature/del"}},"destination":{"branch":{"name":"main"}}}`

	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1}]}`)
		case "/repositories/ws/repo/pullrequests/1":
			fmt.Fprint(w, prJSON)
		case "/repositories/ws/repo/pullrequests/1/comments":
			fmt.Fprint(w, `{"values":[]}`)
		case "/repositories/ws/repo/pullrequests/1/activity":
			fmt.Fprint(w, `{"values":[]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	var importCalled bool
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// IssueExists check (idempotency): not yet migrated → 404
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues":
			importCalled = true
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, terminalImportResponse)
		default:
			t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{BitbucketRPS: 1000})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !importCalled {
		t.Error("expected Import API fallback when branch is deleted, but it was not called")
	}
}

// TestMigrator_gapFill: when PR #1 and #3 exist (gap at #2) and FillGaps=true,
// the Import API is called 3 times (PR#1 + placeholder#2 + PR#3).
func TestMigrator_gapFill(t *testing.T) {
	const pr1JSON = `{"id":1,"title":"PR 1","state":"MERGED","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00"}`
	const pr3JSON = `{"id":3,"title":"PR 3","state":"MERGED","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00"}`

	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1},{"id":3}]}`)
		case "/repositories/ws/repo/pullrequests/1":
			fmt.Fprint(w, pr1JSON)
		case "/repositories/ws/repo/pullrequests/3":
			fmt.Fprint(w, pr3JSON)
		case "/repositories/ws/repo/pullrequests/1/comments",
			"/repositories/ws/repo/pullrequests/3/comments":
			fmt.Fprint(w, `{"values":[]}`)
		case "/repositories/ws/repo/pullrequests/1/activity",
			"/repositories/ws/repo/pullrequests/3/activity":
			fmt.Fprint(w, `{"values":[]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	var importCount atomic.Int32
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// IssueExists check (idempotency): not yet migrated → 404
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues" {
			importCount.Add(1)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, terminalImportResponse)
			return
		}
		t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{
		BitbucketRPS: 1000,
		FillGaps:     true,
	})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := importCount.Load(); got != 3 {
		t.Errorf("expected Import API called 3 times (PR#1 + placeholder#2 + PR#3), got %d", got)
	}
}

// TestMigrator_dryRun: with DryRun=true, no write requests are made to GitHub.
// The branch check (read-only GET) is still performed.
func TestMigrator_dryRun(t *testing.T) {
	const prJSON = `{"id":1,"title":"Add feature","state":"OPEN","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00","source":{"branch":{"name":"feature/add"}},"destination":{"branch":{"name":"main"}}}`

	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1}]}`)
		case "/repositories/ws/repo/pullrequests/1":
			fmt.Fprint(w, prJSON)
		case "/repositories/ws/repo/pullrequests/1/comments":
			fmt.Fprint(w, `{"values":[]}`)
		case "/repositories/ws/repo/pullrequests/1/activity":
			fmt.Fprint(w, `{"values":[]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	var writeAttempted bool
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// IssueExists check is a read-only GET, allowed even in dry-run.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
			rawBranch := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/branches/")
			gotBranch, _ := url.PathUnescape(rawBranch)
			if gotBranch != "feature/add" {
				t.Errorf("branch check: got %q, want feature/add", gotBranch)
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"name":"feature/add"}`)
		default:
			writeAttempted = true
			t.Errorf("dry-run must not make write requests: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{
		BitbucketRPS: 1000,
		DryRun:       true,
	})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if writeAttempted {
		t.Error("write was attempted in dry-run mode (explicit check)")
	}

	report := m.DryRunReport()
	if report.Total() != 1 {
		t.Errorf("expected 1 dry-run entry, got %d", report.Total())
	}
	if got := report.CountByAction(pipeline.ActionGitHubPR); got != 1 {
		t.Errorf("expected 1 ActionGitHubPR entry, got %d", got)
	}
	if len(report.Entries) > 0 {
		e := report.Entries[0]
		if e.PRNumber != 1 {
			t.Errorf("entry PRNumber = %d, want 1", e.PRNumber)
		}
		if e.Head != "feature/add" {
			t.Errorf("entry Head = %q, want feature/add", e.Head)
		}
		if e.Body == "" {
			t.Error("entry Body should not be empty")
		}
	}
}

// TestMigrator_skipAlreadyMigrated: when GitHub already has Issue #1,
// the migrator skips it without calling the Import API or fetching PR details.
func TestMigrator_skipAlreadyMigrated(t *testing.T) {
	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1}]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	var importCalled bool
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// IssueExists check: already migrated → 200
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/issues/1":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"number":1,"title":"Fix bug"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues":
			importCalled = true
			t.Error("Import API must not be called when issue already exists")
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, terminalImportResponse)
		default:
			t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{BitbucketRPS: 1000})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if importCalled {
		t.Error("Import API was called despite issue already existing on GitHub")
	}
}
