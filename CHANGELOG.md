# CHANGELOG

## [Unreleased]

### Added
- `internal/githubapi`: GitHub REST API クライアント（BranchExists / CreatePullRequest / CreateIssueComment）
- OPEN PR のうちソースブランチが GitHub 側に存在するものを本物の GitHub PR として再作成する機能
- PR 作成失敗時は Issue Import API へのフォールバック
- `transform.BuildPRBody`（公開化）と `transform.BuildCommentBodies`（新規追加）
- `BitbucketConfig.APIBase` フィールドを追加（テスト用サーバー URL の注入に対応）
- `internal/transform/links_test.go`: URL書き換え・@mention書き換えの単体テスト 10件
- `internal/pipeline/migrator_test.go`: httptest モックによる統合テスト 5シナリオ（テスト件数 11件 → 28件）
- `--dry-run` 実行時に変換結果のプレビューと件数サマリーを stdout に出力（`-v` で本文プレビューも表示）
- `internal/pipeline/report.go`: `DryRunAction` / `DryRunEntry` / `DryRunReport` 型と集計メソッドを追加

## [0.1.0] - 2026-05-06

### Added
- Makefile（build / test / lint / vet / clean / install）
- TODO.md（作業予定・進捗管理）
- CHANGELOG.md（更新履歴）
- 初期実装
  - Bitbucket Cloud REST API クライアント（PR一覧・詳細・コメント・アクティビティ取得）
  - GitHub Issue Import API クライアント（golden-comet API）
  - Bitbucket PR → GitHub Issue 変換ロジック（メタデータ埋め込み・Markdownリンク変換）
  - パイプライン（番号空間整合のための gap-fill・直列処理）
  - CLIエントリポイント（`--config` / `--repo` / `--all` / `--dry-run` / `--verbose`）
  - TOML 設定ファイル（Bitbucket/GitHub 認証・ユーザーマッピング・リポジトリマッピング）
  - `config.example.toml` 雛形
