# 設計書: 冪等な移行（重複 Issue/PR 防止）

**作成日**: 2026-06-24  
**ステータス**: 承認済み

## 背景と課題

`prmigrate` を複数回実行すると、同じ Bitbucket PR に対して GitHub Issue または GitHub PR が重複して作成されてしまう。移行を途中で中断した場合や、設定確認のために再実行したい場合に問題となる。

## 目標

何度実行しても GitHub 側の状態が変わらない冪等な移行を実現する。

## 設計

### 冪等性の実現方針

移行ループで番号 `n` を処理する前に、GitHub に Issue/PR `#n` がすでに存在するかをチェックする。存在する場合はスキップして次の番号に進む。

既存の設計（Bitbucket PR #N → GitHub Issue/PR #N の番号アラインメント）を利用するため、番号で一意に対応が取れる。Issue Import・GitHub PR・プレースホルダーのすべてに対して同一のチェックで判定できる。

### 変更点

#### 1. `internal/githubapi/client.go` — `IssueExists` メソッド追加

```
GET /repos/{owner}/{repo}/issues/{n}
- 200 → true（存在する）
- 404 → false（存在しない）
- その他 → error
```

GitHub では PR も Issue エンドポイントで取得できるため、このメソッド1本で統一的に判定できる。

#### 2. `internal/pipeline/migrator.go` — ループ内に存在チェック追加

`Run()` のメインループで各番号を処理する直前に `IssueExists` を呼ぶ。

```go
for n := 1; n <= maxID; n++ {
    exists, err := m.ghapi.IssueExists(ctx, n)
    if err != nil {
        m.log.Warn("issue existence check failed, proceeding", "n", n, "err", err)
    } else if exists {
        m.log.Info("skipping: already exists on GitHub", "n", n)
        continue
    }
    // 既存の gap-fill / migrateOne 処理
}
```

**エラー時の挙動**: `IssueExists` が error を返した場合は警告を出してスキップせずに処理を続行する（既存のエラー方針と一致）。

**ドライラン時の挙動**: 存在チェックは実際の GitHub API を呼ぶ（読み取りのみ）。書き込みは行わない。スキップされる番号はログに出力される。

### テスト

| ファイル | 追加内容 |
|---|---|
| `internal/githubapi/client_test.go` | `IssueExists` のユニットテスト（200/404/500 の3ケース） |
| `internal/pipeline/migrator_test.go` | 「既存 Issue はスキップされる」シナリオ追加 |

### 変更ファイル一覧

| ファイル | 変更内容 |
|---|---|
| `internal/githubapi/client.go` | `IssueExists` メソッド追加 |
| `internal/githubapi/types.go` | 必要に応じて Issue 型追加 |
| `internal/githubapi/client_test.go` | `IssueExists` のテスト追加 |
| `internal/pipeline/migrator.go` | ループ内に存在チェック追加 |
| `internal/pipeline/migrator_test.go` | スキップシナリオのテスト追加 |

## 考慮事項

- **API コール増加**: PR 総数と同じ回数の追加 API コールが発生する（例: 100件の PR → +100回の GET）。GitHub REST API のレート制限（認証済みで5000回/時）には収まる想定。
- **番号アラインメント前提**: 本設計は Bitbucket PR #N = GitHub Issue #N の対応が維持されている前提。GitHub 側に他の手段で Issue が作成されており番号がずれている場合は誤スキップが起こりうる。
- **競合状態**: 並列実行は非対応（既存設計と同じ）。
