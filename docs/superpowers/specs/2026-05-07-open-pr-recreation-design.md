# Open PR 再作成機能 設計ドキュメント

**Date:** 2026-05-07
**Scope:** Bitbucket の OPEN 状態 PR のうち、ソースブランチが GitHub 側に存在するものを GitHub PR API で本物の PR として復元する。

---

## 背景と目的

現在のパイプラインはすべての PR（OPEN / MERGED / DECLINED / SUPERSEDED）を GitHub Issue Import API で Issue として取り込む。これにより Bitbucket の PR 番号と GitHub の Issue 番号が一致する。

ただし OPEN PR はブランチが生きている場合、Issue ではなく本物の GitHub PR として復元した方がレビューを継続できる。本機能はその復元を行う。

---

## 設計方針

### 番号整合性

GitHub の Issue と PR は同じ番号空間を共有する。パイプラインは現在直列処理（Submit → Wait → 次へ）なので、OPEN PR の番号枠でも順番通りに処理すれば番号が一致する。

- Import API（非同期）→ `SubmitAndWait` で完了を待つ
- GitHub PR API（同期）→ レスポンスに番号が即時返る

両者を直列処理すれば番号空間の整合性が保たれる。

### 失敗時のフォールバック

GitHub PR API での作成が失敗した場合（差分なし、ブランチ保護違反など）は Import API にフォールバックして Issue として取り込む。番号整合性は維持される。

---

## アーキテクチャ

```
internal/
  bitbucket/       (既存: 変更なし)
  config/          (既存: 変更なし)
  githubimport/    (既存: 変更なし)
  githubapi/       (新規)
    client.go      - BranchExists / CreatePullRequest / CreateIssueComment
    types.go       - リクエスト/レスポンス型
  transform/       (変更: buildPRBody を公開メソッドに昇格)
  pipeline/        (変更: ghapi フィールド追加 + migrateOne に分岐追加)
```

---

## 詳細設計

### `internal/githubapi/types.go`

```go
type CreatePullRequestRequest struct {
    Title string `json:"title"`
    Body  string `json:"body"`
    Head  string `json:"head"` // source branch name
    Base  string `json:"base"` // destination branch name
}

type PullRequest struct {
    Number  int    `json:"number"`
    HTMLURL string `json:"html_url"`
}
```

### `internal/githubapi/client.go`

```go
type Client struct { /* apiBase, repo, token, http.Client */ }

func NewClient(apiBase, repo, token string) *Client

// BranchExists は GET /repos/{owner}/{repo}/branches/{branch} で存在確認。
// 404 は false, nil を返す。その他エラーは error を返す。
func (c *Client) BranchExists(ctx context.Context, branch string) (bool, error)

// CreatePullRequest は POST /repos/{owner}/{repo}/pulls で PR を作成する。
func (c *Client) CreatePullRequest(ctx context.Context, req *CreatePullRequestRequest) (*PullRequest, error)

// CreateIssueComment は POST /repos/{owner}/{repo}/issues/{number}/comments でコメントを追加する。
// PR はコメント API を Issue と共有するため、この API で PR にもコメントを追加できる。
func (c *Client) CreateIssueComment(ctx context.Context, issueNumber int, body string) error
```

### `internal/transform/pr.go` の変更

`buildPRBody` を `BuildPRBody` として公開する（シグネチャは変わらず）。pipeline 側が body を組み立てて `githubapi.CreatePullRequestRequest` を構築する。

### `pipeline/migrator.go` の変更

`Migrator` に `ghapi *githubapi.Client` フィールドを追加。

`migrateOne` の制御フロー：

```
PR を Bitbucket から取得
  │
  ├─ OPEN かつ source.Branch != nil
  │    │
  │    ├─ BranchExists(source.Branch.Name) == true
  │    │    │
  │    │    ├─ CreatePullRequest 成功
  │    │    │    └─ コメントを CreateIssueComment で順番に追加 → 完了
  │    │    │
  │    │    └─ CreatePullRequest 失敗（差分なし等）
  │    │         └─ Import API にフォールバック（body に失敗理由を注記）
  │    │
  │    └─ BranchExists == false（ブランチ削除済み）または source.Branch == nil
  │         └─ Import API（body に「移行時点でブランチが削除済み」を注記）
  │
  └─ CLOSED (MERGED / DECLINED / SUPERSEDED)
       └─ 既存フロー（Import API）
```

### コメントの扱い

GitHub PR API はコメントを一括で作成できないため、PR 作成後に `CreateIssueComment` で順番に POST する。

- タイムスタンプは保存できない（Issues API の制約）
- コメント本文には既存の Import API コメントと同じ形式（元の著者・日時を本文に埋め込む）を使う

### DryRun 対応

`cfg.Tuning.DryRun == true` の場合、`BranchExists` の呼び出しは行い（読み取り専用）、`CreatePullRequest` と `CreateIssueComment` はスキップしてログのみ出力する。

---

## エラーハンドリング

| 状況 | 対応 |
|------|------|
| `BranchExists` がエラー | WARN ログ → Import API にフォールバック（body に「移行時点でブランチ確認に失敗」を注記） |
| `CreatePullRequest` が失敗 | WARN ログ（失敗理由付き）→ Import API にフォールバック |
| `CreateIssueComment` が失敗 | WARN ログのみ（PR 自体は作成済みなので中断しない） |
| PR 番号が Bitbucket PR 番号と不一致 | 既存の WARN ログ（`issue number mismatch`）と同様に警告 |

---

## テスト方針

- `githubapi.Client` のユニットテストは `httptest.Server` でモック
- `BranchExists`・`CreatePullRequest`・`CreateIssueComment` の正常系・異常系（404, 422, 5xx）をカバー
- `transform.BuildPRBody` は既存の `buildPRBody` テストを公開メソッドに合わせて更新

---

## 対象外（スコープ外）

- レビュアーの引き継ぎ（GitHub PR の `requested_reviewers` 設定）
- PR のドラフト状態の再現
- Bitbucket の PR ステータスチェックの移行
