# ドライラン出力改善 設計ドキュメント

**Date:** 2026-05-09
**Scope:** `--dry-run` 実行時に変換結果をプレビュー表示する機能の追加。

---

## 背景と目的

現在の `--dry-run` は slog 構造化ログを stderr に出力するのみ：

```
time=... level=INFO msg="dry-run: would import PR" pr=1 title="Fix bug" comments=3 body_bytes=512
time=... level=INFO msg="dry-run: would create GitHub PR" pr=2 head=feature/add base=main
```

これでは以下が確認できない：
- 移行後の Issue / PR がどんな見た目になるか（変換済み Markdown body）
- 全体でどの経路（GitHub PR / Issue Import / Placeholder）が何件になるか

本タスクでこれらを stdout に出力し、実行前のレビューを容易にする。

---

## 設計

### データ構造（`internal/pipeline/report.go` 新規作成）

```go
package pipeline

type DryRunAction string

const (
    ActionGitHubPR    DryRunAction = "github-pr"
    ActionIssueImport DryRunAction = "issue-import"
    ActionPlaceholder DryRunAction = "placeholder"
)

// DryRunEntry は1件のPRに対して実行予定のアクションを記録する。
type DryRunEntry struct {
    PRNumber     int
    Title        string
    Action       DryRunAction
    State        string // "OPEN" / "MERGED" / etc.（placeholder は空）
    Head         string // ActionGitHubPR のみ
    Base         string // ActionGitHubPR のみ
    CommentCount int
    Body         string // 変換済み Markdown body
}

// DryRunReport は Run() 完了後に取得できる実行予定の集計。
type DryRunReport struct {
    Entries []DryRunEntry
}

func (r *DryRunReport) CountByAction(a DryRunAction) int
func (r *DryRunReport) Total() int
```

### Migrator の変更（`internal/pipeline/migrator.go`）

- `Migrator` に `report DryRunReport` フィールドを追加
- `DryRun=true` の各分岐でエントリを `m.report.Entries` に追加
  - `migrateOne` の DryRun 分岐（Issue Import パス）
  - `tryCreateGitHubPR` の DryRun 分岐（GitHub PR パス）
  - `submitPlaceholder` の DryRun 分岐
- `Run()` のシグネチャは変えない（後方互換を維持）
- `DryRunReport() DryRunReport` メソッドを公開

```go
func (m *Migrator) DryRunReport() DryRunReport {
    return m.report
}
```

### CLI の変更（`cmd/prmigrate/main.go`）

- `Run()` 後、`cfg.Tuning.DryRun=true` の場合に `m.DryRunReport()` を取得し stdout に出力
- 出力先: **stdout**（ログは引き続き stderr）
- `--verbose`（`-v`）フラグとの組み合わせで詳細度を制御：

**`--dry-run` のみ（サマリーのみ）:**

```
=== Dry Run: ws/repo → org/repo ===
  GitHub PR (branch exists):       3
  Issue Import (merged/fallback):  8
  Placeholder (gap fill):          1
  ─────────────────────────────────
  Total:                           12
```

**`--dry-run -v`（本文プレビュー + サマリー）:**

```
── #1 Fix bug [Issue Import / MERGED] ──────────────────────────────
> **Pull request** :twisted_rightwards_arrows: created by @gh-alice on 2024-01-10 09:00 UTC
> State: **`MERGED`**
> Source: `feature/fix` @ [`abc1234`](...)
> Destination: `main`

Fix the null pointer when config is missing.

────────────────────────────────────────────────────────────────────

── #2 Add feature [GitHub PR: feature/add → main] ──────────────────
> **Pull request** :twisted_rightwards_arrows: created by @gh-bob on 2024-01-11 10:00 UTC
> State: **`OPEN`**

Add new feature X.

────────────────────────────────────────────────────────────────────

=== Dry Run Summary: ws/repo → org/repo ===
  GitHub PR (branch exists):      1
  Issue Import (merged/fallback): 1
  Placeholder (gap fill):         0
  ────────────────────────────────
  Total:                          2
```

---

## ファイル構成

| ファイル | 変更種別 | 内容 |
|---------|---------|------|
| `internal/pipeline/report.go` | 新規作成 | `DryRunEntry` / `DryRunReport` / `CountByAction` / `Total` |
| `internal/pipeline/migrator.go` | 変更 | `report` フィールド追加、各 DryRun 分岐でエントリ追加、`DryRunReport()` メソッド追加 |
| `cmd/prmigrate/main.go` | 変更 | `Run()` 後にレポートを stdout へ出力する `printDryRunReport()` 関数追加 |
| `internal/pipeline/report_test.go` | 新規作成 | `CountByAction` / `Total` の単体テスト |
| `internal/pipeline/migrator_test.go` | 変更 | 既存 `TestMigrator_dryRun` を拡張し `DryRunReport()` の件数を検証 |

---

## スコープ外

- ドライラン結果のファイル出力（`> output.md` でリダイレクト可能なため不要）
- JSON 形式出力
- ドライラン時の GitHub トークン不要化（既存動作を維持）
