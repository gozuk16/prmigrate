# 設計仕様：Bitbucket PR データのローカルキャッシュ

Date: 2026-06-25

## 概要

Bitbucket からの PR 取得は PR 数が多い場合に長時間かかる。MERGED・DECLINED・SUPERSEDED（クローズ済み）の PR は状態が変化しないため、一度取得したデータをローカルにキャッシュして再取得を省略する。

## スコープ

### 変更するもの

- `internal/bitbucket/cache.go`（新規）：`Fetcher` インターフェース・`CachedClient`・キャッシュ用バンドル型を定義
- `internal/pipeline/migrator.go`：`bb` フィールドを `*bitbucket.Client` → `bitbucket.Fetcher` に変更
- `cmd/prmigrate/main.go`：`CachedClient` を組み立てて `pipeline.New()` に渡す

### 変更しないもの

- `internal/bitbucket/client.go` — 既存の HTTP クライアントはそのまま
- `internal/bitbucket/types.go` — 型定義はそのまま
- `internal/pipeline/migrator.go` の migrator ロジック — フィールド型変更のみ

## キャッシュファイルの構造

```
<config.toml と同じディレクトリ>/
  cache/
    workspace_repo/         ← bbRepo の "/" を "_" に置換
      1.json
      2.json
      ...
```

各ファイルは PR 1件分のバンドル（PR 本体・コメント・アクティビティ）をまとめた JSON：

```json
{
  "pr":       { ... PullRequest ... },
  "comments": [ ... ],
  "activity": [ ... ]
}
```

## キャッシュの有効条件

| PR の state | キャッシュ対象 |
|---|---|
| MERGED | ✅ |
| DECLINED | ✅ |
| SUPERSEDED | ✅ |
| OPEN | ❌（毎回 API から取得） |

## Fetcher インターフェース

`internal/bitbucket/cache.go` に定義する。`*Client` と `*CachedClient` の両方がこのインターフェースを実装する。

```go
type Fetcher interface {
    ListPullRequestIDs(ctx context.Context) ([]int, error)
    GetPullRequest(ctx context.Context, id int) (*PullRequest, error)
    ListComments(ctx context.Context, prID int) ([]Comment, error)
    ListActivity(ctx context.Context, prID int) ([]Activity, error)
}
```

## CachedClient の設計

```go
type CachedClient struct {
    inner    *Client
    cacheDir string
    loaded   map[int]*cachedBundle
}

type cachedBundle struct {
    PR       PullRequest `json:"pr"`
    Comments []Comment   `json:"comments"`
    Activity []Activity  `json:"activity"`
}

func NewCachedClient(inner *Client, cacheDir string) *CachedClient
```

### GetPullRequest の動作

1. `<cacheDir>/<id>.json` が存在する → ファイルを読んで `loaded[id]` にセット、`&bundle.PR` を返す
2. ファイルが存在しない → `inner.GetPullRequest` で API 取得
   - state が MERGED/DECLINED/SUPERSEDED → コメント・アクティビティも API 取得して `<id>.json` に保存
   - state が OPEN → PR だけ返す（保存しない）

### ListComments の動作

1. `loaded[id]` が存在する → `loaded[id].Comments` を返す
2. 存在しない → `inner.ListComments` で API 取得

### ListActivity の動作

1. `loaded[id]` が存在する → `loaded[id].Activity` を返す
2. 存在しない → `inner.ListActivity` で API 取得

### ListPullRequestIDs の動作

キャッシュしない。常に `inner.ListPullRequestIDs` を呼ぶ。

## エラーハンドリング

| 状況 | 挙動 |
|---|---|
| キャッシュファイルの読み込み失敗（壊れた JSON など） | キャッシュを無視して API から再取得（フォールバック） |
| キャッシュファイルの書き込み失敗 | エラーを返して処理を止める |
| キャッシュディレクトリの作成失敗 | エラーを返す（`NewCachedClient` の初回 mkdir で検出） |

## CLI への組み込み

`main.go` でキャッシュディレクトリを `config.toml` のパスから自動決定する。新しいフラグは追加しない。

```go
cacheBase := filepath.Join(filepath.Dir(*configPath), "cache")
for _, pair := range targets {
    bb, gh := pair[0], pair[1]
    cacheDir := filepath.Join(cacheBase, strings.ReplaceAll(bb, "/", "_"))
    bbClient := bitbucket.NewCachedClient(
        bitbucket.NewClient(cfg.Bitbucket.APIBase, bb, auth, rps),
        cacheDir,
    )
    m := pipeline.New(cfg, bbClient, gh, log)
    ...
}
```

`pipeline.New()` の第3引数（現在 `bbRepo string`）は変えず、`bb` クライアントを引数として渡す形に変更する。

## pipeline.New() のシグネチャ変更

```go
// 変更前
func New(cfg *config.Config, bbRepo, ghRepo string, log *slog.Logger) *Migrator

// 変更後
func New(cfg *config.Config, bb bitbucket.Fetcher, ghRepo string, log *slog.Logger) *Migrator
```

`BitbucketRepo` フィールドは `bb` から取得できないため、`Fetcher` インターフェースに含めるか、引数として別途渡す。→ **`bbRepo string` を引数に残す**（`bb` と `bbRepo` を両方渡す）。

```go
// 確定シグネチャ
func New(cfg *config.Config, bb bitbucket.Fetcher, bbRepo, ghRepo string, log *slog.Logger) *Migrator
```

## テスト方針

- `CachedClient` の単体テスト（`internal/bitbucket/cache_test.go`）：
  - キャッシュヒット：2回目の呼び出しで API が呼ばれないことを確認
  - キャッシュミス（OPEN PR）：毎回 API が呼ばれることを確認
  - 壊れた JSON フォールバック：API から再取得されることを確認
  - キャッシュ書き込みエラー：エラーが返ることを確認
- `Fetcher` インターフェースにより既存の `pipeline` テストはそのまま維持可能
