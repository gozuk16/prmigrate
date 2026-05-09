# ドライラン出力改善 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `--dry-run` 実行時に変換結果のプレビューと件数サマリーを stdout に出力し、実行前のレビューを容易にする。

**Architecture:** `internal/pipeline/report.go` に `DryRunReport` 型を追加し、`Migrator` が各 dry-run 分岐でエントリを蓄積する。CLI は `Run()` 後に `DryRunReport()` を取得し `printDryRunReport()` で stdout に整形出力する。slog ログは引き続き stderr へ。

**Tech Stack:** Go 1.22、標準 `fmt` / `strings` / `io` パッケージのみ

---

## ファイル構成

| ファイル | 変更種別 | 責務 |
|---------|---------|------|
| `internal/pipeline/report.go` | 新規作成 | `DryRunAction` / `DryRunEntry` / `DryRunReport` 型と集計メソッド |
| `internal/pipeline/report_test.go` | 新規作成 | `CountByAction` / `Total` の単体テスト |
| `internal/pipeline/migrator.go` | 変更 | `report` フィールド、各 DryRun 分岐でエントリ追加、`DryRunReport()` メソッド公開 |
| `internal/pipeline/migrator_test.go` | 変更 | 既存 `TestMigrator_dryRun` を拡張して `DryRunReport()` の件数・内容を検証 |
| `cmd/prmigrate/main.go` | 変更 | `printDryRunReport()` 関数追加、`Run()` 後に呼び出し |

---

### Task 1: DryRunReport 型を作成

**Files:**
- Create: `internal/pipeline/report.go`
- Create: `internal/pipeline/report_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/pipeline/report_test.go
package pipeline_test

import (
	"testing"

	"github.com/gozuk16/prmigrate/internal/pipeline"
)

func TestDryRunReport_CountByAction(t *testing.T) {
	r := pipeline.DryRunReport{
		Entries: []pipeline.DryRunEntry{
			{Action: pipeline.ActionGitHubPR},
			{Action: pipeline.ActionIssueImport},
			{Action: pipeline.ActionIssueImport},
			{Action: pipeline.ActionPlaceholder},
		},
	}
	if got := r.CountByAction(pipeline.ActionGitHubPR); got != 1 {
		t.Errorf("CountByAction(github-pr) = %d, want 1", got)
	}
	if got := r.CountByAction(pipeline.ActionIssueImport); got != 2 {
		t.Errorf("CountByAction(issue-import) = %d, want 2", got)
	}
	if got := r.CountByAction(pipeline.ActionPlaceholder); got != 1 {
		t.Errorf("CountByAction(placeholder) = %d, want 1", got)
	}
}

func TestDryRunReport_Total(t *testing.T) {
	r := pipeline.DryRunReport{
		Entries: []pipeline.DryRunEntry{
			{Action: pipeline.ActionGitHubPR},
			{Action: pipeline.ActionIssueImport},
		},
	}
	if got := r.Total(); got != 2 {
		t.Errorf("Total() = %d, want 2", got)
	}
}

func TestDryRunReport_Empty(t *testing.T) {
	var r pipeline.DryRunReport
	if got := r.Total(); got != 0 {
		t.Errorf("Total() on empty = %d, want 0", got)
	}
	if got := r.CountByAction(pipeline.ActionGitHubPR); got != 0 {
		t.Errorf("CountByAction on empty = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/pipeline/...
```

Expected: FAIL — `pipeline.DryRunReport`, `pipeline.ActionGitHubPR` などが未定義

- [ ] **Step 3: Implement report.go**

```go
// internal/pipeline/report.go
package pipeline

// DryRunAction describes what would happen to a single Bitbucket PR.
type DryRunAction string

const (
	ActionGitHubPR    DryRunAction = "github-pr"
	ActionIssueImport DryRunAction = "issue-import"
	ActionPlaceholder DryRunAction = "placeholder"
)

// DryRunEntry records the planned action for one PR.
type DryRunEntry struct {
	PRNumber     int
	Title        string
	Action       DryRunAction
	State        string // "OPEN" / "MERGED" / etc. (empty for placeholder)
	Head         string // ActionGitHubPR only: source branch
	Base         string // ActionGitHubPR only: destination branch
	CommentCount int
	Body         string // transformed Markdown body
}

// DryRunReport collects planned actions after a dry-run Run().
type DryRunReport struct {
	Entries []DryRunEntry
}

// CountByAction returns the number of entries with the given action.
func (r *DryRunReport) CountByAction(a DryRunAction) int {
	n := 0
	for _, e := range r.Entries {
		if e.Action == a {
			n++
		}
	}
	return n
}

// Total returns the total number of entries.
func (r *DryRunReport) Total() int {
	return len(r.Entries)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./internal/pipeline/...
```

Expected: 3 new tests PASS（既存の5件も引き続きPASS）

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/report.go internal/pipeline/report_test.go
git commit -m "feat(pipeline): add DryRunReport type for dry-run result collection"
```

---

### Task 2: Migrator にエントリ蓄積と DryRunReport() メソッドを追加

**Files:**
- Modify: `internal/pipeline/migrator.go`
- Modify: `internal/pipeline/migrator_test.go`

- [ ] **Step 1: Write the failing test additions**

`internal/pipeline/migrator_test.go` の `TestMigrator_dryRun` 関数の末尾（`if writeAttempted { ... }` の後）に以下を追加する:

```go
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
```

また `TestMigrator_mergedPR` の末尾に追加する:

```go
	report := m.DryRunReport()
	if report.Total() != 0 {
		t.Errorf("non-dry-run should have empty report, got %d entries", report.Total())
	}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/pipeline/...
```

Expected: FAIL — `m.DryRunReport` は未定義

- [ ] **Step 3: Update migrator.go**

`Migrator` 構造体に `report` フィールドを追加する（`internal/pipeline/migrator.go` の struct 定義部分）:

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
	report DryRunReport
}
```

`migrateOne` の DryRun 分岐（現在の133〜138行目）を次のように置き換える:

```go
	if m.Cfg.Tuning.DryRun {
		m.log.Info("dry-run: would import PR", "pr", prID,
			"title", pr.Title,
			"comments", len(req.Comments),
			"body_bytes", len(req.Issue.Body))
		m.report.Entries = append(m.report.Entries, DryRunEntry{
			PRNumber:     prID,
			Title:        pr.Title,
			Action:       ActionIssueImport,
			State:        pr.State,
			CommentCount: len(req.Comments),
			Body:         req.Issue.Body,
		})
		return nil
	}
```

`tryCreateGitHubPR` の DryRun 分岐（現在の177〜181行目）を次のように置き換える:

```go
	if m.Cfg.Tuning.DryRun {
		body := m.xfmr.BuildPRBody(pr)
		m.log.Info("dry-run: would create GitHub PR",
			"pr", pr.ID, "head", pr.Source.Branch.Name, "base", pr.Destination.Branch.Name)
		m.report.Entries = append(m.report.Entries, DryRunEntry{
			PRNumber: pr.ID,
			Title:    pr.Title,
			Action:   ActionGitHubPR,
			State:    pr.State,
			Head:     pr.Source.Branch.Name,
			Base:     pr.Destination.Branch.Name,
			Body:     body,
		})
		return true, nil
	}
```

`submitPlaceholder` の DryRun 分岐（現在の225〜228行目）を次のように置き換える:

```go
	if m.Cfg.Tuning.DryRun {
		m.log.Info("dry-run: would create placeholder", "n", n)
		m.report.Entries = append(m.report.Entries, DryRunEntry{
			PRNumber: n,
			Title:    fmt.Sprintf("Deleted Bitbucket PR #%d", n),
			Action:   ActionPlaceholder,
			Body:     "_This Bitbucket pull request number was missing or deleted at migration time._",
		})
		return nil
	}
```

ファイル末尾に `DryRunReport()` メソッドを追加する:

```go
// DryRunReport returns the collected dry-run entries after Run() completes.
// Returns an empty report if DryRun was not enabled.
func (m *Migrator) DryRunReport() DryRunReport {
	return m.report
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./internal/pipeline/...
```

Expected: 全テスト（8件）PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/migrator.go internal/pipeline/migrator_test.go
git commit -m "feat(pipeline): accumulate DryRunReport entries in Migrator"
```

---

### Task 3: CLI に printDryRunReport を追加

**Files:**
- Modify: `cmd/prmigrate/main.go`

- [ ] **Step 1: Add printDryRunReport function and call site**

`cmd/prmigrate/main.go` の import に `io` と `strings` を追加する:

```go
import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gozuk16/prmigrate/internal/config"
	"github.com/gozuk16/prmigrate/internal/pipeline"
)
```

`main()` のループ部分を次のように変更する（`m.Run(ctx)` の後に DryRun 出力を追加）:

```go
	for _, pair := range targets {
		bb, gh := pair[0], pair[1]
		log.Info("starting repo migration", "bb", bb, "gh", gh)
		m := pipeline.New(cfg, bb, gh, log)
		if err := m.Run(ctx); err != nil {
			log.Error("repo migration failed", "bb", bb, "gh", gh, "err", err)
		}
		if cfg.Tuning.DryRun {
			printDryRunReport(os.Stdout, bb, gh, m.DryRunReport(), *verbose)
		}
	}
```

`fail` 関数の前に `printDryRunReport` 関数を追加する:

```go
func printDryRunReport(w io.Writer, bbRepo, ghRepo string, report pipeline.DryRunReport, verbose bool) {
	const divider = "────────────────────────────────────────────────────────────────────────"

	if verbose {
		for _, e := range report.Entries {
			switch e.Action {
			case pipeline.ActionGitHubPR:
				fmt.Fprintf(w, "\n── #%d %s [GitHub PR: %s → %s] ──\n", e.PRNumber, e.Title, e.Head, e.Base)
			case pipeline.ActionIssueImport:
				fmt.Fprintf(w, "\n── #%d %s [Issue Import / %s] ──\n", e.PRNumber, e.Title, e.State)
			case pipeline.ActionPlaceholder:
				fmt.Fprintf(w, "\n── #%d %s [Placeholder] ──\n", e.PRNumber, e.Title)
			}
			fmt.Fprintln(w, e.Body)
			fmt.Fprintln(w, divider)
		}
	}

	fmt.Fprintf(w, "\n=== Dry Run: %s → %s ===\n", bbRepo, ghRepo)
	fmt.Fprintf(w, "  GitHub PR (branch exists):       %d\n", report.CountByAction(pipeline.ActionGitHubPR))
	fmt.Fprintf(w, "  Issue Import (merged/fallback):  %d\n", report.CountByAction(pipeline.ActionIssueImport))
	fmt.Fprintf(w, "  Placeholder (gap fill):          %d\n", report.CountByAction(pipeline.ActionPlaceholder))
	fmt.Fprintln(w, "  "+strings.Repeat("─", 33))
	fmt.Fprintf(w, "  Total:                           %d\n", report.Total())
}
```

- [ ] **Step 2: Verify build passes**

```
go build ./...
```

Expected: コンパイルエラーなし

- [ ] **Step 3: Run all tests**

```
go test ./...
```

Expected: 全テスト PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/prmigrate/main.go
git commit -m "feat(cli): print dry-run report to stdout after Run()"
```

---

## Self-Review

### Spec coverage

| Spec 要件 | タスク |
|---|---|
| `DryRunEntry` / `DryRunReport` 型の定義 | Task 1 |
| `CountByAction` / `Total` メソッド | Task 1 |
| Migrator が各 DryRun 分岐でエントリを蓄積 | Task 2 |
| `DryRunReport()` メソッドの公開 | Task 2 |
| `Run()` シグネチャ変更なし | Task 2（変更なし）|
| CLI がサマリーを stdout に出力（`--dry-run` のみ） | Task 3 |
| CLI が本文プレビューを stdout に出力（`--dry-run -v`） | Task 3 |
| 出力先が stdout / ログが stderr のまま | Task 3 |

### Placeholder scan

なし — 全ステップに実際のコードが含まれている。

### Type consistency

- `DryRunEntry.Action` は `DryRunAction` 型（string）で Task 1 で定義、Task 2 / Task 3 で使用 ✅
- `DryRunReport.CountByAction(a DryRunAction)` は Task 1 で定義、Task 3 の `printDryRunReport` 内で使用 ✅
- `m.DryRunReport()` は Task 2 で定義、Task 3（main.go）で呼び出し ✅
- `report.Entries` スライスへの直接アクセスはテストのみ（Task 2）、CLI は `CountByAction` / `Total` のみ使用 ✅
