# テストカバレッジ追加 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `transform/links.go` のURL書き換え・メンション書き換えの単体テスト6件と、`pipeline/migrator.go` の主要フロー5シナリオを httptest モックで網羅し、リグレッション検知を可能にする。

**Architecture:** 本番コードへの最小変更として `BitbucketConfig` に `APIBase` フィールドを追加し `bitbucket.NewClient` を更新する。これにより `pipeline.New` でテスト用サーバー URL を注入できるようになる。テストは外部パッケージ (`transform_test`, `pipeline_test`) として書き、`httptest.NewServer` で各 API をモックする。

**Tech Stack:** Go 1.22、`net/http/httptest`、標準 `testing` パッケージ

---

### Task 1: BitbucketConfig に APIBase フィールドを追加

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/config/config_test.go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/config/...
```

Expected: FAIL — `cfg.Bitbucket.APIBase undefined (type config.BitbucketConfig has no field or method APIBase)`

- [ ] **Step 3: Add APIBase to BitbucketConfig and update ApplyDefaults**

`internal/config/config.go` の `BitbucketConfig` を次のように変更する:

```go
type BitbucketConfig struct {
	APIBase  string `toml:"api_base"` // default: https://api.bitbucket.org/2.0
	Username string `toml:"username"`
	Token    string `toml:"token"` // app password or API token
}
```

`ApplyDefaults()` の先頭に Bitbucket APIBase のデフォルト設定を追加する（GitHub の設定より前）:

```go
func (c *Config) ApplyDefaults() {
	if c.Bitbucket.APIBase == "" {
		c.Bitbucket.APIBase = "https://api.bitbucket.org/2.0"
	}
	if c.GitHub.APIBase == "" {
		c.GitHub.APIBase = "https://api.github.com"
	}
	if c.Tuning.BitbucketRPS == 0 {
		c.Tuning.BitbucketRPS = 0.25
	}
	if c.StateLabels == nil {
		c.StateLabels = map[string]string{
			"OPEN":       "",
			"MERGED":     "merged",
			"DECLINED":   "declined",
			"SUPERSEDED": "superseded",
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./internal/config/...
```

Expected: PASS (2 new tests)

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add APIBase to BitbucketConfig with default"
```

---

### Task 2: bitbucket.NewClient シグネチャ変更と pipeline.New の更新

**Files:**
- Modify: `internal/bitbucket/client.go:36-43`
- Modify: `internal/pipeline/migrator.go:51`

- [ ] **Step 1: Update bitbucket.NewClient to accept apiBase**

`internal/bitbucket/client.go` の `NewClient` 関数を次のように変更する:

```go
// NewClient creates a client for the specified repository (e.g. "workspace/repo").
// apiBase: e.g. "https://api.bitbucket.org/2.0" (or a test httptest server URL).
// rps is the maximum sustained request rate.
func NewClient(apiBase, repoFullName string, auth Auth, rps float64) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    fmt.Sprintf("%s/repositories/%s", apiBase, repoFullName),
		auth:       auth,
		limiter:    rate.NewLimiter(rate.Limit(rps), 1),
	}
}
```

- [ ] **Step 2: Update pipeline.New to pass cfg.Bitbucket.APIBase**

`internal/pipeline/migrator.go` の `New` 関数の `bitbucket.NewClient` 呼び出し（51行目付近）を次のように変更する:

```go
bb: bitbucket.NewClient(cfg.Bitbucket.APIBase, bbRepo, bitbucket.Auth{Username: cfg.Bitbucket.Username, Token: cfg.Bitbucket.Token}, cfg.Tuning.BitbucketRPS),
```

- [ ] **Step 3: Verify the build and tests pass**

```
go build ./...
go test ./...
```

Expected: すべてのパッケージがコンパイルされ、既存のテストがすべて PASS

- [ ] **Step 4: Commit**

```bash
git add internal/bitbucket/client.go internal/pipeline/migrator.go
git commit -m "feat(bitbucket): accept apiBase in NewClient for testability"
```

---

### Task 3: transform/links_test.go 作成

**Files:**
- Create: `internal/transform/links_test.go`

`rewriteBody`（private）は公開 API の `BuildPRBody` 経由でテストする。`pr_test.go` と同じ `transform_test` パッケージなので `newTestTransformer()` を再利用できる。

- [ ] **Step 1: Write the failing tests**

```go
// internal/transform/links_test.go
package transform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
)

// makePRWithDesc は links_test 専用ヘルパー。Author を nil にすることで
// ヘッダー部の @mention が本文の検証を汚染しないようにする。
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
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/transform/...
```

Expected: FAIL — テストファイルはコンパイルされるが6件すべて失敗（`makePRWithDesc` はまだ存在しない）

実際にはファイルを作成した直後なのでコンパイルエラーなしで FAIL するはず。もしコンパイルエラーがあれば修正してから進む。

- [ ] **Step 3: Run tests to verify they pass**

`links.go` の実装は既に完成しているため、テストファイルを作成するだけでパスする。

```
go test ./internal/transform/...
```

Expected: PASS (6 new tests + 3 existing tests = 9 total)

- [ ] **Step 4: Commit**

```bash
git add internal/transform/links_test.go
git commit -m "test(transform): add URL and mention rewrite tests for links.go"
```

---

### Task 4: pipeline/migrator_test.go 作成

**Files:**
- Create: `internal/pipeline/migrator_test.go`

3つの httptest.Server（bbSrv: Bitbucket モック、ghSrv: GitHub Import API + REST API の統合モック）を使って5シナリオをテストする。

Import API mock は POST で即座に `"status":"imported"` を返すことで `SubmitAndWait` のポーリングループを回避する。

- [ ] **Step 1: Write the failing tests**

```go
// internal/pipeline/migrator_test.go
package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/pipeline"
)

// newTestMigrator は bbURL / ghURL を注入したテスト用 Migrator を返す。
// tuning.BitbucketRPS は必ず 1000 以上に設定してレート制限の待機を排除すること。
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

// terminalImportResponse は Import API の即時完了レスポンスを返す。
// IssueURL の末尾は IssueNumber() が使う issue 番号。
const terminalImportResponse = `{"status":"imported","issue_url":"http://example.com/repos/org/repo/issues/1","url":"http://example.com/status/1"}`

// TestMigrator_mergedPR: MERGED PR 1件 → Import API に closed=true / labels=merged で送信される
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
		if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues" {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &got) //nolint:errcheck
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
}

// TestMigrator_openPR_branchExists: OPEN PR でソースブランチが存在 → GitHub PR API で PR 作成
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
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
			// BranchExists — ブランチあり
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"name":"feature/add"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/pulls":
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &gotPR) //nolint:errcheck
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

// TestMigrator_openPR_branchDeleted: OPEN PR でブランチが 404 → Import API にフォールバック
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
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
			// ブランチなし
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

// TestMigrator_gapFill: PR#1 と PR#3 のみ存在（#2 欠番）、FillGaps=true → Import API が 3 回呼ばれる
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

// TestMigrator_dryRun: DryRun=true → Import API / PR API への書き込みリクエストが一切発生しない
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

	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
			// BranchExists は DryRun でも呼ばれる（読み取りのみ）
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"name":"feature/add"}`)
		default:
			// POST が来たらテスト失敗
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
	// Write リクエストが来た場合は ghSrv ハンドラ内の t.Errorf が発火する
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/pipeline/...
```

Expected: FAIL — `pipeline.New` or `pipeline.Migrator` が見つからない、または `config.BitbucketConfig.APIBase` が未定義（Task 1&2 が完了していれば）。
Task 1&2 完了済みであれば、コンパイルは通るがテストが失敗する。

- [ ] **Step 3: Run tests to verify they pass**

`pipeline.New` と各モック API は既に実装済みのためテストはパスするはず。

```
go test ./internal/pipeline/...
```

Expected: PASS (5 new tests)

もしタイムアウトしてテストが止まる場合は `go test -timeout 30s ./internal/pipeline/...` で確認する。

- [ ] **Step 4: Run all tests to confirm nothing is broken**

```
go test ./...
```

Expected: すべて PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/migrator_test.go
git commit -m "test(pipeline): add httptest integration tests for 5 migration scenarios"
```

---

## Self-Review

### Spec coverage

| Spec requirement | Task |
|---|---|
| `BitbucketConfig` に `APIBase` フィールド追加 | Task 1 |
| `ApplyDefaults()` にデフォルト設定追加 | Task 1 |
| `bitbucket.NewClient` シグネチャ変更 | Task 2 |
| `pipeline.New` で `cfg.Bitbucket.APIBase` を渡す | Task 2 |
| `TestRewriteBody_pullRequestURL` | Task 3 |
| `TestRewriteBody_issueURL` | Task 3 |
| `TestRewriteBody_commitURL` | Task 3 |
| `TestRewriteBody_unmappedRepo` | Task 3 |
| `TestRewriteBody_mappedMention` | Task 3 |
| `TestRewriteBody_unmappedMention` | Task 3 |
| `TestMigrator_mergedPR` | Task 4 |
| `TestMigrator_openPR_branchExists` | Task 4 |
| `TestMigrator_openPR_branchDeleted` | Task 4 |
| `TestMigrator_gapFill` | Task 4 |
| `TestMigrator_dryRun` | Task 4 |

すべての spec 要件がカバーされている。

### Placeholder scan

プレースホルダーなし — すべてのステップに実際のコードが含まれている。

### Type consistency

- `config.BitbucketConfig.APIBase`: Task 1 で定義、Task 2 で `cfg.Bitbucket.APIBase` として参照 ✓
- `bitbucket.NewClient(apiBase, ...)`: Task 2 で定義、pipeline.New で呼び出し ✓
- `newTestMigrator`: Task 4 で定義し、5つのテスト内で使用 ✓
- `terminalImportResponse`: Task 4 の定数、4テストで使用 ✓
- `SubmitAndWait` polling: POST で即座に `"status":"imported"` を返すことで、`initial.IsTerminal()` = true となりポーリング不要 ✓
