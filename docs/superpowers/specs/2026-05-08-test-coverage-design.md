# テストカバレッジ追加 設計ドキュメント

**Date:** 2026-05-08
**Scope:** transform パッケージの単体テスト追加と、pipeline パッケージの httptest を使った統合テスト追加。

---

## 背景と目的

現在テストが存在するのは `internal/githubapi/`（8件）と `internal/transform/` の一部（3件）のみ。以下が未テスト：

- `transform/links.go` の URL 書き換え・メンション書き換えロジック
- `pipeline/migrator.go` の主要フロー（OPEN PR / MERGED PR / フォールバック / gap fill）

本タスクでこれらにテストを追加し、リグレッション検知を可能にする。

---

## 本番コードへの最小変更

### `internal/config/config.go`

`BitbucketConfig` に `APIBase` フィールドを追加（GitHub config と同じパターン）:

```go
type BitbucketConfig struct {
    APIBase  string `toml:"api_base"` // default: https://api.bitbucket.org/2.0
    Username string `toml:"username"`
    Token    string `toml:"token"`
}
```

`ApplyDefaults()` にデフォルト設定を追加:

```go
if c.Bitbucket.APIBase == "" {
    c.Bitbucket.APIBase = "https://api.bitbucket.org/2.0"
}
```

### `internal/bitbucket/client.go`

`NewClient` のシグネチャを変更:

```go
// 変更前
func NewClient(repoFullName string, auth Auth, rps float64) *Client

// 変更後
func NewClient(apiBase, repoFullName string, auth Auth, rps float64) *Client
```

`baseURL` の構築を `apiBase` から:

```go
baseURL: fmt.Sprintf("%s/repositories/%s", apiBase, repoFullName),
```

### `internal/pipeline/migrator.go`

`New()` で `cfg.Bitbucket.APIBase` を渡す:

```go
bb: bitbucket.NewClient(cfg.Bitbucket.APIBase, bbRepo, bitbucket.Auth{...}, cfg.Tuning.BitbucketRPS),
```

---

## テストファイル

### `internal/transform/links_test.go`

`BuildPRBody` に description を含む PR を渡して URL 書き換えとメンションを検証。プライベート関数を直接呼ばず、公開 API 経由でテスト。

| テスト名 | 検証内容 |
|---------|---------|
| `TestRewriteBody_pullRequestURL` | `bitbucket.org/ws/repo/pull-requests/5` → `github.com/org/repo/pull/5` |
| `TestRewriteBody_issueURL` | `bitbucket.org/ws/repo/issues/3` → `github.com/org/repo/issues/3` |
| `TestRewriteBody_commitURL` | `bitbucket.org/ws/repo/commits/abc123` → `github.com/org/repo/commit/abc123` |
| `TestRewriteBody_unmappedRepo` | マッピングなしの URL は変更されない |
| `TestRewriteBody_mappedMention` | `@alice` → `@gh-alice` |
| `TestRewriteBody_unmappedMention` | `@unknown` → `unknown`（@なし） |

### `internal/pipeline/migrator_test.go`

3 つの httptest.Server を立ち上げ:
1. **Bitbucket mock**: PR 一覧・詳細・コメント・アクティビティを返す
2. **GitHub Import API mock**: Issue Import の submit と poll（`status: "imported"`）を返す
3. **GitHub REST API mock**: branch check・PR 作成・コメント作成を返す

`cfg.Bitbucket.APIBase` と `cfg.GitHub.APIBase` をテストサーバー URL に向けて `pipeline.New()` で Migrator を構築し、`Run()` を呼ぶ。

| テスト名 | シナリオ | 検証内容 |
|---------|---------|---------|
| `TestMigrator_mergedPR` | MERGED PR 1件 | Import API POST が呼ばれ、`closed=true`, `labels=["pull-request","merged"]` |
| `TestMigrator_openPR_branchExists` | OPEN PR 1件、ブランチあり | GitHub PR API POST が呼ばれ、`head` / `base` が正しい |
| `TestMigrator_openPR_branchDeleted` | OPEN PR 1件、ブランチなし（404） | Import API POST にフォールバック |
| `TestMigrator_gapFill` | PR#1 と PR#3（#2 欠番）、FillGaps=true | Import API が 3 回呼ばれ、2 件目は placeholder タイトル |
| `TestMigrator_dryRun` | OPEN PR 1件、DryRun=true | Import API・PR API へ書き込みリクエストが発生しない |

---

## 実装方針

- テストは外部パッケージ（`pipeline_test`, `transform_test`）として書く
- `httptest.NewServer` で各 API をモック
- Import API の非同期ポーリングは、POST でステータス URL を返し、GET でそれを即時 `imported` として返す
- pipeline テストの DryRun 検証は、POST が来たら即テスト失敗するハンドラを設定することで保証する

---

## スコープ外

- `bitbucket` パッケージ自体のユニットテスト（API 呼び出しのテスト）
- `githubimport` パッケージのユニットテスト
- `config` パッケージのユニットテスト
