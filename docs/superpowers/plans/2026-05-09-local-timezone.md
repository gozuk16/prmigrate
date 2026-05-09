# ローカルタイムゾーン表示 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 移行後の Issue / PR 本文に埋め込む日時を UTC 固定から実行環境のローカルタイムゾーンに変更する。

**Architecture:** `internal/transform/pr.go` の `formatDate` 関数1箇所のみ変更。`t.UTC()` を `t.Local()` に、フォーマット文字列の `"UTC"` リテラルを Go の動的タイムゾーン動詞 `"MST"` に置き換える。

**Tech Stack:** Go 1.22 標準ライブラリのみ（`time` パッケージ）

---

## ファイル構成

| ファイル | 変更種別 | 責務 |
|---------|---------|------|
| `internal/transform/pr.go` | 変更 | `formatDate` 関数の実装を `t.Local()` + `"MST"` に変更 |
| `internal/transform/pr_test.go` | 変更 | ローカルタイムゾーン変換を検証するテストを追加 |

---

### Task 1: formatDate をローカルタイムゾーンに変更

**Files:**
- Modify: `internal/transform/pr.go:273-275`
- Modify: `internal/transform/pr_test.go`

- [ ] **Step 1: 失敗するテストを追加**

`internal/transform/pr_test.go` の末尾に以下を追加する:

```go
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
```

- [ ] **Step 2: テストが失敗することを確認**

```
go test ./internal/transform/...
```

Expected: `TestBuildPRBody_dateUsesLocalTimezone` が FAIL（現実装は `09:00 UTC` を出力するため）

- [ ] **Step 3: formatDate を実装**

`internal/transform/pr.go:273-275` を以下に変更する:

```go
func formatDate(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04 MST")
}
```

- [ ] **Step 4: テストが通ることを確認**

```
go test ./internal/transform/...
```

Expected: 全テスト PASS（`TestBuildPRBody_dateUsesLocalTimezone` を含む）

- [ ] **Step 5: コミット**

```bash
git add internal/transform/pr.go internal/transform/pr_test.go
git commit -m "feat(transform): use local timezone in date formatting"
```

---

## Self-Review

### Spec coverage

| Spec 要件 | タスク |
|---|---|
| `formatDate` が `time.Local` を使う | Task 1 Step 3 |
| タイムゾーン略称が動的（`MST` 動詞） | Task 1 Step 3 |
| UTC 固定の廃止 | Task 1 Step 3 |

### Placeholder scan

なし — 全ステップに実際のコードが含まれている。

### Type consistency

変更は `formatDate` 関数1箇所のみで、シグネチャ変更なし ✅
