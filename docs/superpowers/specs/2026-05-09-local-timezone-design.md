# ローカルタイムゾーン表示 設計ドキュメント

**Date:** 2026-05-09
**Scope:** 移行後の Issue / PR 本文に埋め込む日時を UTC 固定から実行環境のローカルタイムゾーンに変更する。

---

## 背景と目的

現在の `formatDate` は常に UTC で出力する：

```
Pull request 🔀 created by @gozuk16 on 2026-05-09 04:15 UTC
Last updated on 2026-05-09 04:16 UTC
```

JST 環境で実行した場合でも UTC になるため、実際の操作時刻と 9 時間ずれて見づらい。実行環境の OS タイムゾーンに合わせて表示する。

---

## 設計

### 変更箇所

`internal/transform/pr.go` の `formatDate` 関数のみ。

```go
// Before
func formatDate(t time.Time) string {
    return t.UTC().Format("2006-01-02 15:04 UTC")
}

// After
func formatDate(t time.Time) string {
    return t.Local().Format("2006-01-02 15:04 MST")
}
```

- `t.UTC()` → `t.Local()`：OS の `time.Local` ロケーションで変換
- フォーマット中の `"UTC"` リテラル → Go フォーマット動詞 `"MST"`：実際のタイムゾーン略称（`JST`、`EST` 等）を動的出力

### 出力例（JST 環境）

```
Pull request 🔀 created by @gozuk16 on 2026-05-09 13:15 JST
Last updated on 2026-05-09 13:16 JST
```

---

## スコープ外

- `config.toml` による timezone 設定（YAGNI：OS タイムゾーンで十分）
- UTC での保存や変換ロジックへの影響なし（表示のみの変更）
