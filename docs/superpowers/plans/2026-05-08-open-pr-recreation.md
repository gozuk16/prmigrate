# Open PR 再作成機能 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bitbucket の OPEN PR のうちソースブランチが GitHub 側に存在するものを、GitHub PR API で本物の PR として再作成する。

**Architecture:** `internal/githubapi/` パッケージに GitHub REST API クライアント（BranchExists / CreatePullRequest / CreateIssueComment）を新設。`pipeline.Migrator` が OPEN PR 検出時にブランチ存在確認を行い、生きていれば PR API を、削除済みまたは PR 作成失敗時は既存の Issue Import API にフォールバックする。直列処理を維持するため番号整合性は保たれる。

**Tech Stack:** Go 1.22, 標準ライブラリ（`net/http`, `encoding/json`, `net/http/httptest`）

---

## ファイル構成

| 操作 | パス | 責務 |
|------|------|------|
| 新規作成 | `internal/githubapi/types.go` | リクエスト/レスポンス型定義 |
| 新規作成 | `internal/githubapi/client.go` | GitHub REST API クライアント |
| 新規作成 | `internal/githubapi/client_test.go` | クライアントのユニットテスト |
| 変更 | `internal/transform/pr.go` | `buildPRBody` を公開、`BuildCommentBodies` 追加 |
| 新規作成 | `internal/transform/pr_test.go` | transform のユニットテスト |
| 変更 | `internal/pipeline/migrator.go` | `ghapi` フィールド追加、`tryCreateGitHubPR` 追加 |

---

## Task 1: `internal/githubapi/types.go` を作成する

**Files:**
- Create: `internal/githubapi/types.go`

- [ ] **Step 1: ファイルを作成する**

```go
// Package githubapi implements a client for the GitHub REST API v3.
// This package handles branch existence checks and pull request creation,
// complementing the githubimport package which uses the Issue Import API.
package githubapi

// CreatePullRequestRequest is the JSON body for POST /repos/{owner}/{repo}/pulls.
type CreatePullRequestRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"` // source branch name
	Base  string `json:"base"` // destination branch name
}

// PullRequest is the relevant subset of GitHub's pull request response.
type PullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}
```

- [ ] **Step 2: コンパイル確認**

```bash
go build ./internal/githubapi/...
```

期待: エラーなし（パッケージが空なのでビルド対象なし、ファイル単体でのエラーチェック）

```bash
go vet ./internal/githubapi/...
```

期待: エラーなし

- [ ] **Step 3: コミット**

```bash
git add internal/githubapi/types.go
git commit -m "feat: add githubapi types for PR creation"
```

---

## Task 2: `BranchExists` を実装する（TDD）

**Files:**
- Create: `internal/githubapi/client.go`
- Create: `internal/githubapi/client_test.go`

- [ ] **Step 1: テストを書く（失敗することを確認するため先に書く）**

`internal/githubapi/client_test.go` を作成:

```go
package githubapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gozuk16/prmigrate/internal/githubapi"
)

func TestBranchExists_found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/branches/feature-x" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"name":"feature-x"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	exists, err := c.BranchExists(context.Background(), "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected branch to exist")
	}
}

func TestBranchExists_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	exists, err := c.BranchExists(context.Background(), "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected branch to not exist")
	}
}

func TestBranchExists_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	_, err := c.BranchExists(context.Background(), "feature-x")
	if err == nil {
		t.Error("expected error for 5xx response")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

```bash
go test ./internal/githubapi/... -run TestBranchExists -v
```

期待: コンパイルエラー（`NewClient` と `BranchExists` が未定義）

- [ ] **Step 3: `NewClient` と `BranchExists` を実装する**

`internal/githubapi/client.go` を作成:

```go
package githubapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client targets a single GitHub repository's REST API endpoints.
type Client struct {
	httpClient *http.Client
	baseURL    string // .../repos/{owner}/{repo}
	token      string
}

// NewClient creates a client for the specified repository.
//   - apiBase: e.g. "https://api.github.com"
//   - repoFullName: "owner/repo"
//   - token: a PAT or fine-grained token with contents:read and pull-requests:write scope.
func NewClient(apiBase, repoFullName, token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    fmt.Sprintf("%s/repos/%s", apiBase, repoFullName),
		token:      token,
	}
}

// BranchExists reports whether the named branch exists in the repository.
// A 404 response returns (false, nil); other non-200 responses return an error.
func (c *Client) BranchExists(ctx context.Context, branch string) (bool, error) {
	url := fmt.Sprintf("%s/branches/%s", c.baseURL, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("branch check failed: %s", resp.Status)
	}
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "prmigrate/0.1")
}
```

- [ ] **Step 4: テストが通ることを確認する**

```bash
go test ./internal/githubapi/... -run TestBranchExists -v
```

期待:
```
--- PASS: TestBranchExists_found (0.00s)
--- PASS: TestBranchExists_notFound (0.00s)
--- PASS: TestBranchExists_serverError (0.00s)
PASS
```

- [ ] **Step 5: コミット**

```bash
git add internal/githubapi/client.go internal/githubapi/client_test.go
git commit -m "feat: add githubapi.Client with BranchExists"
```

---

## Task 3: `CreatePullRequest` を実装する（TDD）

**Files:**
- Modify: `internal/githubapi/client.go`
- Modify: `internal/githubapi/client_test.go`

- [ ] **Step 1: テストを追加する**

`internal/githubapi/client_test.go` の末尾に追加:

```go
func TestCreatePullRequest_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/pulls" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number":5,"html_url":"https://github.com/org/repo/pull/5"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	pr, err := c.CreatePullRequest(context.Background(), &githubapi.CreatePullRequestRequest{
		Title: "Fix bug",
		Body:  "description",
		Head:  "feature-x",
		Base:  "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 5 {
		t.Errorf("expected PR number 5, got %d", pr.Number)
	}
	if pr.HTMLURL != "https://github.com/org/repo/pull/5" {
		t.Errorf("unexpected HTMLURL: %s", pr.HTMLURL)
	}
}

func TestCreatePullRequest_validationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"message":"No commits between main and feature-x"}]}`)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	_, err := c.CreatePullRequest(context.Background(), &githubapi.CreatePullRequestRequest{
		Title: "Fix bug",
		Body:  "description",
		Head:  "feature-x",
		Base:  "main",
	})
	if err == nil {
		t.Error("expected error for 422 response")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

```bash
go test ./internal/githubapi/... -run TestCreatePullRequest -v
```

期待: コンパイルエラー（`CreatePullRequest` が未定義）

- [ ] **Step 3: `CreatePullRequest` を実装する**

`internal/githubapi/client.go` に以下の import と関数を追加する。

import ブロックを以下に更新:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)
```

`BranchExists` の後に追加:

```go
// CreatePullRequest creates a GitHub pull request and returns it.
// Returns an error for non-201 responses (e.g. 422 when head == base).
func (c *Client) CreatePullRequest(ctx context.Context, prReq *CreatePullRequestRequest) (*PullRequest, error) {
	body, err := json.Marshal(prReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/pulls", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create PR failed: %s: %s", resp.Status, string(respBody))
	}

	var pr PullRequest
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &pr, nil
}
```

- [ ] **Step 4: テストが通ることを確認する**

```bash
go test ./internal/githubapi/... -run TestCreatePullRequest -v
```

期待:
```
--- PASS: TestCreatePullRequest_success (0.00s)
--- PASS: TestCreatePullRequest_validationError (0.00s)
PASS
```

- [ ] **Step 5: コミット**

```bash
git add internal/githubapi/client.go internal/githubapi/client_test.go
git commit -m "feat: add CreatePullRequest to githubapi.Client"
```

---

## Task 4: `CreateIssueComment` を実装する（TDD）

**Files:**
- Modify: `internal/githubapi/client.go`
- Modify: `internal/githubapi/client_test.go`

- [ ] **Step 1: テストを追加する**

`internal/githubapi/client_test.go` の末尾に追加:

```go
func TestCreateIssueComment_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/issues/5/comments" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":101}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	err := c.CreateIssueComment(context.Background(), 5, "great PR!")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateIssueComment_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	err := c.CreateIssueComment(context.Background(), 5, "great PR!")
	if err == nil {
		t.Error("expected error for 5xx response")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

```bash
go test ./internal/githubapi/... -run TestCreateIssueComment -v
```

期待: コンパイルエラー（`CreateIssueComment` が未定義）

- [ ] **Step 3: `CreateIssueComment` を実装する**

`internal/githubapi/client.go` の `CreatePullRequest` の後に追加:

```go
// CreateIssueComment posts a comment on a GitHub issue or pull request.
// PRs and Issues share the same comment endpoint on GitHub.
func (c *Client) CreateIssueComment(ctx context.Context, issueNumber int, commentBody string) error {
	payload := struct {
		Body string `json:"body"`
	}{Body: commentBody}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/issues/%d/comments", c.baseURL, issueNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create comment failed: %s: %s", resp.Status, string(respBody))
	}
	return nil
}
```

- [ ] **Step 4: 全テストが通ることを確認する**

```bash
go test ./internal/githubapi/... -v
```

期待: 全 7 テストが PASS

- [ ] **Step 5: コミット**

```bash
git add internal/githubapi/client.go internal/githubapi/client_test.go
git commit -m "feat: add CreateIssueComment to githubapi.Client"
```

---

## Task 5: `transform` パッケージに `BuildPRBody` と `BuildCommentBodies` を公開する（TDD）

**Files:**
- Modify: `internal/transform/pr.go`
- Create: `internal/transform/pr_test.go`

- [ ] **Step 1: テストを書く**

`internal/transform/pr_test.go` を作成:

```go
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
		"@gh-alice",           // mapped user mention
		"#3",                  // original PR ID
		"OPEN",                // state
		"feature/add",        // source branch
		"main",               // destination branch
		"This adds a new feature.", // description
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
			CreatedOn: t2, // later timestamp
			UpdatedOn: t2,
			Content:   bitbucket.Content{Raw: "Second comment"},
			User:      &bitbucket.User{Nickname: "alice", DisplayName: "Alice"},
		},
		{
			ID:        2,
			CreatedOn: t1, // earlier timestamp
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
```

- [ ] **Step 2: テストが失敗することを確認する**

```bash
go test ./internal/transform/... -v
```

期待: コンパイルエラー（`BuildPRBody` と `BuildCommentBodies` が未定義）

- [ ] **Step 3: `buildPRBody` を `BuildPRBody` にリネームし `BuildCommentBodies` を追加する**

`internal/transform/pr.go` で以下の変更を行う:

1. `buildPRBody` を `BuildPRBody` にリネームする（関数定義とすべての呼び出し箇所）:

`PullRequestToImport` 内の呼び出し（35行目付近）:
```go
// 変更前
body := t.buildPRBody(pr)
// 変更後
body := t.BuildPRBody(pr)
```

関数定義（76行目付近）:
```go
// 変更前
func (t *Transformer) buildPRBody(pr *bitbucket.PullRequest) string {
// 変更後
func (t *Transformer) BuildPRBody(pr *bitbucket.PullRequest) string {
```

2. ファイル末尾（`commitLink` の後）に `BuildCommentBodies` を追加する:

```go
// BuildCommentBodies returns the Markdown body strings for each non-deleted
// comment and activity entry, sorted chronologically. This is used when
// creating comments via the GitHub REST API (which cannot set timestamps),
// as an alternative to the Issue Import API path.
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
```

- [ ] **Step 4: テストが通ることを確認する**

```bash
go test ./internal/transform/... -v
```

期待: 全テストが PASS

- [ ] **Step 5: プロジェクト全体のビルド確認**

```bash
go build ./...
```

期待: エラーなし

- [ ] **Step 6: コミット**

```bash
git add internal/transform/pr.go internal/transform/pr_test.go
git commit -m "feat: expose BuildPRBody and add BuildCommentBodies to Transformer"
```

---

## Task 6: `pipeline.Migrator` に `tryCreateGitHubPR` を統合する

**Files:**
- Modify: `internal/pipeline/migrator.go`

- [ ] **Step 1: `migrator.go` の import ブロックと `Migrator` 構造体を更新する**

import ブロックに `githubapi` を追加:

```go
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
```

`Migrator` 構造体に `ghapi` フィールドを追加:

```go
type Migrator struct {
	Cfg           *config.Config
	BitbucketRepo string
	GitHubRepo    string

	bb    *bitbucket.Client
	gh    *githubimport.Client
	ghapi *githubapi.Client
	xfmr  *transform.Transformer
	log   *slog.Logger
}
```

`New` 関数に `ghapi` の初期化を追加:

```go
func New(cfg *config.Config, bbRepo, ghRepo string, log *slog.Logger) *Migrator {
	return &Migrator{
		Cfg:           cfg,
		BitbucketRepo: bbRepo,
		GitHubRepo:    ghRepo,
		bb:            bitbucket.NewClient(bbRepo, bitbucket.Auth{Username: cfg.Bitbucket.Username, Token: cfg.Bitbucket.Token}, cfg.Tuning.BitbucketRPS),
		gh:            githubimport.NewClient(cfg.GitHub.APIBase, ghRepo, cfg.GitHub.Token),
		ghapi:         githubapi.NewClient(cfg.GitHub.APIBase, ghRepo, cfg.GitHub.Token),
		xfmr:          transform.New(cfg, bbRepo, ghRepo),
		log:           log.With("bb_repo", bbRepo, "gh_repo", ghRepo),
	}
}
```

- [ ] **Step 2: `migrateOne` に OPEN PR 分岐を追加する**

`migrateOne` 関数を以下に置き換える:

```go
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

	// For OPEN PRs with a living source branch, attempt GitHub PR API.
	if isOpen(pr.State) && pr.Source.Branch != nil && pr.Destination.Branch != nil {
		created, err := m.tryCreateGitHubPR(ctx, pr, comments, activity)
		if err != nil {
			m.log.Warn("GitHub PR creation failed, falling back to Issue Import",
				"pr", prID, "err", err)
		} else if created {
			return nil
		}
	}

	req := m.xfmr.PullRequestToImport(pr, comments, activity)

	if m.Cfg.Tuning.DryRun {
		m.log.Info("dry-run: would import PR", "pr", prID,
			"title", pr.Title,
			"comments", len(req.Comments),
			"body_bytes", len(req.Issue.Body))
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
```

- [ ] **Step 3: `tryCreateGitHubPR` ヘルパーを追加する**

`migrateOne` の後に追加:

```go
// tryCreateGitHubPR attempts to create a real GitHub PR for an OPEN Bitbucket PR.
// Returns (true, nil) on success, (false, nil) if the branch is absent (caller
// should fall back to Issue Import), or (false, err) on unexpected failure.
func (m *Migrator) tryCreateGitHubPR(
	ctx context.Context,
	pr *bitbucket.PullRequest,
	comments []bitbucket.Comment,
	activity []bitbucket.Activity,
) (bool, error) {
	exists, err := m.ghapi.BranchExists(ctx, pr.Source.Branch.Name)
	if err != nil {
		return false, fmt.Errorf("branch check: %w", err)
	}
	if !exists {
		m.log.Info("source branch deleted; will import as Issue",
			"pr", pr.ID, "branch", pr.Source.Branch.Name)
		return false, nil
	}

	if m.Cfg.Tuning.DryRun {
		m.log.Info("dry-run: would create GitHub PR",
			"pr", pr.ID, "head", pr.Source.Branch.Name, "base", pr.Destination.Branch.Name)
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
```

- [ ] **Step 4: ビルドと全テストを確認する**

```bash
go build ./...
```

期待: エラーなし

```bash
go test ./...
```

期待: 全テストが PASS

- [ ] **Step 5: コミット**

```bash
git add internal/pipeline/migrator.go
git commit -m "feat: recreate OPEN PRs via GitHub PR API in pipeline"
```

---

## Task 7: TODO.md と CHANGELOG.md を更新する

**Files:**
- Modify: `.claude/worktrees/happy-yonath-ee55fe/TODO.md` または `TODO.md`（ルートにある方）
- Modify: `CHANGELOG.md`（存在すれば）

- [ ] **Step 1: `TODO.md` を更新する**

TODO.md の `## 未着手` セクションで「Open PR の本物の PR 再作成」を完了に移動する:

```markdown
## 完了

- [x] 初期実装（Bitbucket → GitHub Issue Import API による移行）
  ...（既存）

- [x] Open PR の本物の PR 再作成（`internal/githubapi/` パッケージ）
  - ブランチが生きている Open PR は GitHub PR API で復元
  - ブランチ削除済みの場合は Issue として記録
```

- [ ] **Step 2: `CHANGELOG.md` を更新する（存在しなければ作成する）**

```markdown
## [Unreleased]

### Added
- `internal/githubapi`: GitHub REST API クライアント（BranchExists / CreatePullRequest / CreateIssueComment）
- OPEN PR のうちソースブランチが GitHub 側に存在するものを本物の GitHub PR として再作成する機能
- PR 作成失敗時は Issue Import API へのフォールバック
```

- [ ] **Step 3: コミット**

```bash
git add TODO.md CHANGELOG.md   # または該当ファイル
git commit -m "docs: update TODO and CHANGELOG for Open PR recreation feature"
```
