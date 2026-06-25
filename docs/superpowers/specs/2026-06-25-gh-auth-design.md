# 設計仕様：GitHub トークン管理を gh 認証ストアに委譲

Date: 2026-06-25

## 概要

現在 TOML config ファイルの `github.token` または環境変数 `PRMIGRATE_GITHUB_TOKEN` で管理している GitHub トークンを、`gh` CLI の認証ストア（`gh auth login` で保存されるもの）からも取得できるようにする。

目的はトークンを config ファイルに書かなくて済むようにすること。`githubimport`（golden-comet）は変更しない。

## スコープ

### 変更するもの

- `go.mod` / `go.sum`：`github.com/cli/go-gh/v2` を追加
- `internal/config/config.go`：`ResolveSecrets()` に `cli/go-gh` フォールバックを追加、ホスト名抽出ヘルパーを追加

### 変更しないもの

- `internal/githubapi/` — HTTP クライアントコードはそのまま
- `internal/githubimport/` — golden-comet クライアントはそのまま（トークン必須のままだが、解決手段が増える）
- `internal/pipeline/` — `New()` の引数・呼び出し方は変わらない

### あわせて変更するもの

- `cmd/prmigrate/main.go`：`ResolveSecrets()` がエラーを返すようになるため、呼び出し箇所でエラーハンドリングを追加する

## トークン解決の優先順位

`ResolveSecrets()` 内で以下の順に試みる：

1. TOML config の `github.token`（直接指定）
2. 環境変数 `PRMIGRATE_GITHUB_TOKEN`
3. `cli/go-gh` の認証ストア：`auth.TokenForHost(hostname)` を呼ぶ

3 つすべてが空の場合はエラーを返す（現在は空のままで後続が失敗していた）。

## ホスト名の抽出

`cfg.GitHub.APIBase` の URL からホスト名を抽出し `auth.TokenForHost` に渡す。

| APIBase | `auth.TokenForHost` に渡すホスト名 |
|---|---|
| `https://api.github.com` | `github.com` |
| `https://github.example.com/api/v3` | `github.example.com` |

実装方針：
- `url.Parse` でホスト名を取り出す
- ホスト名が `api.` で始まる場合は先頭の `api.` を除去する（`api.github.com` → `github.com`）
- それ以外はホスト名そのまま（GitHub Enterprise Server の場合）

## エラーメッセージ

すべての手段でトークンが取得できなかった場合：

```
github token not found: set github.token in config, PRMIGRATE_GITHUB_TOKEN env var, or run "gh auth login"
```

`ResolveSecrets()` はエラーを返す型に変更する（現在は `void`）。

## TOML 設定例（変更後）

```toml
[github]
api_base = "https://api.github.com"
# token は省略可能。gh auth login 済みなら不要。
# token = "ghp_xxxx"
```

## 依存関係

- `github.com/cli/go-gh/v2` — `pkg/auth` パッケージのみ使用
- `gh` コマンドのインストール自体は不要（Go ライブラリとして動作）
- ただし認証情報は `gh auth login` で作成されたものを参照する

## テスト方針

- `ResolveSecrets()` の既存テストに、`cli/go-gh` フォールバックのテストを追加
- `auth.TokenForHost` は `GH_TOKEN` 環境変数からも読む（テスト時はこれを使う）
- エラーケース（3 つすべて空）のテストを追加
