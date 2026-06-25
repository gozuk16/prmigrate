# Bitbucket PR データのローカルキャッシュ 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** クローズ済み Bitbucket PR（MERGED/DECLINED/SUPERSEDED）を JSON ファイルにキャッシュし、再実行時の Bitbucket API 呼び出しをゼロにする。

**Architecture:** `bitbucket.Fetcher` インターフェースを導入し、`CachedClient` が `*Client` をラップする。`GetPullRequest` でクローズ済みを検知した時点でコメント・アクティビティもまとめて取得しバンドルとして保存。`pipeline.Migrator` は `Fetcher` インターフェースで受け取るため内部ロジックは無変更。

**Tech Stack:** Go 1.25、標準ライブラリのみ（`encoding/json`、`os`、`path/filepath`）

## Global Constraints

- ブランチ: `feature/bitbucket-cache`
- キャッシュ対象: MERGED・DECLINED・SUPERSEDED のみ（OPEN はキャッシュしない）
- キャッシュファイル: `<config.toml と同じディレクトリ>/cache/<workspace>_<repo>/<prID>.json`
- キャッシュ読み込み失敗（壊れた JSON）→ API から再取得（フォールバック）
- キャッシュ書き込み失敗 → エラーを返す
- `internal/bitbucket/client.go`・`types.go` は変更しない
- `pipeline.New()` の確定シグネチャ: `func New(cfg *config.Config, bb bitbucket.Fetcher, bbRepo, ghRepo string, log *slog.Logger) *Migrator`

---

### Task 1: Fetcher インターフェースと CachedClient の実装

**Files:**
- Create: `internal/bitbucket/cache.go`
- Create: `internal/bitbucket/cache_test.go`

**Interfaces:**
- Produces:
  - `type Fetcher interface` — `*Client` と `*CachedClient` の両方が実装する
  - `func NewCachedClient(inner *Client, cacheDir string) (*CachedClient, error)`

- [ ] **Step 1: テストファイルを作成し、最初のテスト（キャッシュヒット）を書く**

`internal/bitbucket/cache_test.go` を新規作成：

```go
package bitbucket_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
)

// bbHandler は Bitbucket テストサーバーのハンドラを返す。
// apiCalls はリクエスト数をカウントするためのアトミックカウンタ。
func bbHandler(t *testing.T, apiCalls *int32, prJSON, commentsJSON, activityJSON string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(apiCalls, 1)
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/pullrequests/1/comments"):
			fmt.Fprint(w, commentsJSON)
		case strings.HasSuffix(p, "/pullrequests/1/activity"):
			fmt.Fprint(w, activityJSON)
		case strings.HasSuffix(p, "/pullrequests/1"):
			fmt.Fprint(w, prJSON)
		default:
			http.NotFound(w, r)
		}
	}
}

const (
	mergedPRJSON  = `{"id":1,"title":"Fix","state":"MERGED","created_on":"2024-01-01T00:00:00+00:00","updated_on":"2024-01-01T00:00:00+00:00"}`
	openPRJSON    = `{"id":1,"title":"WIP","state":"OPEN","created_on":"2024-01-01T00:00:00+00:00","updated_on":"2024-01-01T00:00:00+00:00"}`
	emptyListJSON = `{"values":[]}`
)

// TestCachedClient_terminalPR_cachedAfterFirstCall は、クローズ済み PR が
// 最初の呼び出し後にキャッシュされ、2回目以降は API を呼ばないことを確認する。
func TestCachedClient_terminalPR_cachedAfterFirstCall(t *testing.T) {
	var apiCalls int32
	srv := httptest.NewServer(bbHandler(t, &apiCalls, mergedPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	// 1回目: API 呼び出し（PR + comments + activity = 3回）
	pr, err := c.GetPullRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.ID != 1 {
		t.Errorf("expected PR ID 1, got %d", pr.ID)
	}
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected 3 API calls after first GetPullRequest, got %d", got)
	}

	// ListComments / ListActivity は loaded map から返るため API 呼び出しなし
	if _, err := c.ListComments(ctx, 1); err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if _, err := c.ListActivity(ctx, 1); err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected still 3 API calls after ListComments/Activity, got %d", got)
	}

	// 2回目: 新しい CachedClient（再起動を模擬）→ キャッシュから読む
	c2, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient (2nd): %v", err)
	}
	pr2, err := c2.GetPullRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPullRequest (2nd): %v", err)
	}
	if pr2.ID != 1 {
		t.Errorf("expected PR ID 1 from cache, got %d", pr2.ID)
	}
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected no new API calls on cache hit, got %d total", got)
	}

	// キャッシュファイルが存在することを確認
	if _, err := os.Stat(filepath.Join(cacheDir, "1.json")); os.IsNotExist(err) {
		t.Error("expected cache file to exist")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

```bash
go test ./internal/bitbucket/... -run TestCachedClient_terminalPR -v
```

Expected: コンパイルエラー（`bitbucket.NewCachedClient` が未定義）

- [ ] **Step 3: `cache.go` を実装する**

`internal/bitbucket/cache.go` を新規作成：

```go
package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Fetcher はパイプラインが Bitbucket から PR データを取得するためのインターフェース。
// *Client と *CachedClient の両方が実装する。
type Fetcher interface {
	ListPullRequestIDs(ctx context.Context) ([]int, error)
	GetPullRequest(ctx context.Context, id int) (*PullRequest, error)
	ListComments(ctx context.Context, prID int) ([]Comment, error)
	ListActivity(ctx context.Context, prID int) ([]Activity, error)
}

// コンパイル時にインターフェースの実装を検証する。
var _ Fetcher = (*Client)(nil)
var _ Fetcher = (*CachedClient)(nil)

// cachedBundle は1つの PR に関するすべてのデータをまとめたキャッシュ単位。
type cachedBundle struct {
	PR       PullRequest `json:"pr"`
	Comments []Comment   `json:"comments"`
	Activity []Activity  `json:"activity"`
}

// CachedClient は *Client をラップし、終端状態（MERGED/DECLINED/SUPERSEDED）の
// PR をローカルファイルにキャッシュする。
type CachedClient struct {
	inner    *Client
	cacheDir string
	loaded   map[int]*cachedBundle
}

// NewCachedClient は CachedClient を作成する。cacheDir が存在しない場合は作成する。
// ディレクトリの作成に失敗した場合はエラーを返す。
func NewCachedClient(inner *Client, cacheDir string) (*CachedClient, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", cacheDir, err)
	}
	return &CachedClient{
		inner:    inner,
		cacheDir: cacheDir,
		loaded:   make(map[int]*cachedBundle),
	}, nil
}

// isTerminalState は PR がキャッシュ対象の終端状態かを返す。
func isTerminalState(state string) bool {
	return state == "MERGED" || state == "DECLINED" || state == "SUPERSEDED"
}

func (c *CachedClient) cachePath(id int) string {
	return filepath.Join(c.cacheDir, fmt.Sprintf("%d.json", id))
}

func (c *CachedClient) loadFromFile(id int) (*cachedBundle, error) {
	data, err := os.ReadFile(c.cachePath(id))
	if err != nil {
		return nil, err
	}
	var b cachedBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *CachedClient) saveToFile(id int, b *cachedBundle) error {
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("marshal cache for PR %d: %w", id, err)
	}
	if err := os.WriteFile(c.cachePath(id), data, 0o644); err != nil {
		return fmt.Errorf("write cache for PR %d: %w", id, err)
	}
	return nil
}

// ListPullRequestIDs は常に inner を呼ぶ（キャッシュしない）。
func (c *CachedClient) ListPullRequestIDs(ctx context.Context) ([]int, error) {
	return c.inner.ListPullRequestIDs(ctx)
}

// GetPullRequest はキャッシュファイルがあればそこから返す。なければ API から取得し、
// 終端状態であればコメント・アクティビティも取得してキャッシュに保存する。
// キャッシュファイルの読み込みに失敗した場合（壊れた JSON 等）は API から再取得する。
func (c *CachedClient) GetPullRequest(ctx context.Context, id int) (*PullRequest, error) {
	if b, err := c.loadFromFile(id); err == nil {
		c.loaded[id] = b
		return &b.PR, nil
	}

	pr, err := c.inner.GetPullRequest(ctx, id)
	if err != nil {
		return nil, err
	}

	if isTerminalState(pr.State) {
		comments, err := c.inner.ListComments(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("fetch comments for cache PR %d: %w", id, err)
		}
		activity, err := c.inner.ListActivity(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("fetch activity for cache PR %d: %w", id, err)
		}
		b := &cachedBundle{PR: *pr, Comments: comments, Activity: activity}
		if err := c.saveToFile(id, b); err != nil {
			return nil, err
		}
		c.loaded[id] = b
	}
	return pr, nil
}

// ListComments は loaded map にエントリがあればキャッシュから返す。なければ API を呼ぶ。
func (c *CachedClient) ListComments(ctx context.Context, prID int) ([]Comment, error) {
	if b, ok := c.loaded[prID]; ok {
		return b.Comments, nil
	}
	return c.inner.ListComments(ctx, prID)
}

// ListActivity は loaded map にエントリがあればキャッシュから返す。なければ API を呼ぶ。
func (c *CachedClient) ListActivity(ctx context.Context, prID int) ([]Activity, error) {
	if b, ok := c.loaded[prID]; ok {
		return b.Activity, nil
	}
	return c.inner.ListActivity(ctx, prID)
}
```

- [ ] **Step 4: 最初のテストが通ることを確認する**

```bash
go test ./internal/bitbucket/... -run TestCachedClient_terminalPR -v
```

Expected: PASS

- [ ] **Step 5: 残り3つのテストを追加する**

`internal/bitbucket/cache_test.go` に追記：

```go
// TestCachedClient_openPR_notCached は OPEN PR がキャッシュされないことを確認する。
func TestCachedClient_openPR_notCached(t *testing.T) {
	var apiCalls int32
	srv := httptest.NewServer(bbHandler(t, &apiCalls, openPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	if _, err := c.GetPullRequest(ctx, 1); err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	// OPEN PR: API 1回のみ（comments/activity は GetPullRequest 内では取得しない）
	if got := atomic.LoadInt32(&apiCalls); got != 1 {
		t.Errorf("expected 1 API call for OPEN PR, got %d", got)
	}
	// キャッシュファイルが存在しないことを確認
	if _, err := os.Stat(filepath.Join(cacheDir, "1.json")); !os.IsNotExist(err) {
		t.Error("expected no cache file for OPEN PR")
	}
}

// TestCachedClient_corruptCache_fallsBackToAPI は壊れた JSON キャッシュの場合に
// API から再取得することを確認する。
func TestCachedClient_corruptCache_fallsBackToAPI(t *testing.T) {
	var apiCalls int32
	srv := httptest.NewServer(bbHandler(t, &apiCalls, mergedPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	// 壊れたキャッシュファイルを事前に配置
	if err := os.WriteFile(filepath.Join(cacheDir, "1.json"), []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	pr, err := c.GetPullRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.ID != 1 {
		t.Errorf("expected PR ID 1, got %d", pr.ID)
	}
	// フォールバック: PR + comments + activity = 3回
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected 3 API calls on corrupt cache fallback, got %d", got)
	}
}

// TestCachedClient_writeError_returnsError はキャッシュ書き込みに失敗した場合に
// エラーが返ることを確認する。
func TestCachedClient_writeError_returnsError(t *testing.T) {
	srv := httptest.NewServer(bbHandler(t, new(int32), mergedPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	// "1.json" をディレクトリとして作成し、同名ファイルへの書き込みを失敗させる
	if err := os.Mkdir(filepath.Join(cacheDir, "1.json"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	_, err = c.GetPullRequest(ctx, 1)
	if err == nil {
		t.Error("expected error when cache write fails, got nil")
	}
}
```

- [ ] **Step 6: 全テストが通ることを確認する**

```bash
go test ./internal/bitbucket/... -v
```

Expected: 4件すべて PASS

- [ ] **Step 7: ビルドが通ることを確認する**

```bash
go build ./...
```

Expected: エラーなし

- [ ] **Step 8: コミットする**

```bash
git add internal/bitbucket/cache.go internal/bitbucket/cache_test.go
git commit -m "feat(bitbucket): add Fetcher interface and CachedClient for PR caching"
```

---

### Task 2: pipeline.Migrator の bb フィールド型変更

**Files:**
- Modify: `internal/pipeline/migrator.go`（`bb` フィールド型変更、`New()` シグネチャ変更）
- Modify: `internal/pipeline/migrator_test.go`（`newTestMigrator` を更新）

**Interfaces:**
- Consumes:
  - `type Fetcher interface`（Task 1 で定義済み）
  - `func NewClient(apiBase, repoFullName string, auth Auth, rps float64) *Client`（既存）
- Produces:
  - `func New(cfg *config.Config, bb bitbucket.Fetcher, bbRepo, ghRepo string, log *slog.Logger) *Migrator`

- [ ] **Step 1: `migrator.go` の `bb` フィールドと `New()` を変更する**

`internal/pipeline/migrator.go` の `Migrator` 構造体と `New()` を変更：

変更前：
```go
type Migrator struct {
    Cfg           *config.Config
    BitbucketRepo string
    GitHubRepo    string

    bb     *bitbucket.Client
    gh     *githubimport.Client
    ghapi  *githubapi.Client
    xfmr   *transform.Transformer
    log    *slog.Logger
    report DryRunReport
}

func New(cfg *config.Config, bbRepo, ghRepo string, log *slog.Logger) *Migrator {
    return &Migrator{
        Cfg:           cfg,
        BitbucketRepo: bbRepo,
        GitHubRepo:    ghRepo,
        bb:            bitbucket.NewClient(cfg.Bitbucket.APIBase, bbRepo, bitbucket.Auth{Username: cfg.Bitbucket.Username, Token: cfg.Bitbucket.Token}, cfg.Tuning.BitbucketRPS),
        gh:            githubimport.NewClient(cfg.GitHub.APIBase, ghRepo, cfg.GitHub.Token),
        ghapi:         githubapi.NewClient(cfg.GitHub.APIBase, ghRepo, cfg.GitHub.Token),
        xfmr:          transform.New(cfg, bbRepo, ghRepo),
        log:           log.With("bb_repo", bbRepo, "gh_repo", ghRepo),
    }
}
```

変更後：
```go
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
```

- [ ] **Step 2: ビルドエラーを確認する**

```bash
go build ./...
```

Expected: `cmd/prmigrate/main.go` と `migrator_test.go` でコンパイルエラー（`New()` の引数不足）

- [ ] **Step 3: `migrator_test.go` の `newTestMigrator` を更新する**

`internal/pipeline/migrator_test.go` の `newTestMigrator` 関数を変更：

変更前：
```go
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
```

変更後：
```go
func newTestMigrator(t *testing.T, bbURL, ghURL string, tuning config.TuningConfig) *pipeline.Migrator {
    t.Helper()
    cfg := &config.Config{
        GitHub: config.GitHubConfig{
            APIBase: ghURL,
            Token:   "gh-token",
        },
        UserMapping: map[string]string{},
        RepoMapping: map[string]string{"ws/repo": "org/repo"},
        Tuning:      tuning,
    }
    cfg.ApplyDefaults()
    bb := bitbucket.NewClient(bbURL, "ws/repo", bitbucket.Auth{Username: "user", Token: "token"}, tuning.BitbucketRPS)
    return pipeline.New(cfg, bb, "ws/repo", "org/repo", slog.Default())
}
```

`migrator_test.go` のインポートに `"github.com/gozuk16/prmigrate/internal/bitbucket"` を追加する。

- [ ] **Step 4: pipeline のテストが通ることを確認する**

```bash
go test ./internal/pipeline/... -v
```

Expected: すべて PASS

- [ ] **Step 5: コミットする**

```bash
git add internal/pipeline/migrator.go internal/pipeline/migrator_test.go
git commit -m "refactor(pipeline): use bitbucket.Fetcher interface in Migrator"
```

---

### Task 3: main.go への CachedClient 組み込みとドキュメント更新

**Files:**
- Modify: `cmd/prmigrate/main.go`
- Modify: `TODO.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes:
  - `func NewCachedClient(inner *Client, cacheDir string) (*CachedClient, error)`（Task 1）
  - `func New(cfg *config.Config, bb bitbucket.Fetcher, bbRepo, ghRepo string, log *slog.Logger) *Migrator`（Task 2）

- [ ] **Step 1: `main.go` を更新する**

`cmd/prmigrate/main.go` のインポートに `"path/filepath"` を追加（`"strings"` は既存）。

ターゲットループを変更：

変更前：
```go
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
```

変更後：
```go
cacheBase := filepath.Join(filepath.Dir(*configPath), "cache")
for _, pair := range targets {
    bbRepo, ghRepo := pair[0], pair[1]
    log.Info("starting repo migration", "bb", bbRepo, "gh", ghRepo)
    cacheDir := filepath.Join(cacheBase, strings.ReplaceAll(bbRepo, "/", "_"))
    bbClient, err := bitbucket.NewCachedClient(
        bitbucket.NewClient(cfg.Bitbucket.APIBase, bbRepo, bitbucket.Auth{
            Username: cfg.Bitbucket.Username,
            Token:    cfg.Bitbucket.Token,
        }, cfg.Tuning.BitbucketRPS),
        cacheDir,
    )
    if err != nil {
        fail(log, "init cache", err)
    }
    m := pipeline.New(cfg, bbClient, bbRepo, ghRepo, log)
    if err := m.Run(ctx); err != nil {
        log.Error("repo migration failed", "bb", bbRepo, "gh", ghRepo, "err", err)
    }
    if cfg.Tuning.DryRun {
        report := m.DryRunReport()
        printDryRunReport(os.Stdout, bbRepo, ghRepo, report, *verbose)
    }
}
```

`main.go` のインポートに `"github.com/gozuk16/prmigrate/internal/bitbucket"` を追加する。

- [ ] **Step 2: ビルドが通ることを確認する**

```bash
go build ./...
```

Expected: エラーなし

- [ ] **Step 3: 全テストを実行する**

```bash
go test ./...
```

Expected: すべて PASS

- [ ] **Step 4: `TODO.md` を更新する**

`TODO.md` の「完了」セクションに追記：

```markdown
- [x] Bitbucket PR データのローカルキャッシュ（MERGED/DECLINED/SUPERSEDED を JSON ファイルにキャッシュして再取得を省略）
```

- [ ] **Step 5: `CHANGELOG.md` を更新する**

`CHANGELOG.md` の `[Unreleased]` セクションに追記：

```markdown
### Added
- Bitbucket PR データのローカルキャッシュ機能を追加。MERGED・DECLINED・SUPERSEDED の PR は `<config.toml 配置ディレクトリ>/cache/<repo>/` に JSON ファイルとして保存され、再実行時の Bitbucket API 呼び出しをゼロにする。
```

- [ ] **Step 6: コミットする**

```bash
git add cmd/prmigrate/main.go TODO.md CHANGELOG.md
git commit -m "feat(cmd): wire up CachedClient for Bitbucket PR caching; update docs"
```
