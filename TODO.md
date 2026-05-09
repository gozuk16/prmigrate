# TODO

## 完了

- [x] 初期実装（Bitbucket → GitHub Issue Import API による移行）
  - Bitbucket Cloud REST API クライアント
  - GitHub Issue Import API クライアント（golden-comet）
  - PR → Issue 変換ロジック
  - パイプライン（番号空間整合・gap-fill）
  - CLI エントリポイント（urfave/cli/v3）
  - TOML 設定ファイル読み込み

- [x] Open PR の本物の PR 再作成（`internal/githubapi/` パッケージ）
  - ブランチが生きている Open PR は GitHub PR API で復元
  - ブランチ削除済みの場合は Issue として記録

## 未着手

- [x] テスト追加
  - transform パッケージの単体テスト（URL書き換え・@mention書き換え 10件）
  - pipeline パッケージの統合テスト（httptest モック 5シナリオ）
- [x] ドライラン時の出力改善（変換結果のプレビュー表示）
- [ ] レート制限の調整（Bitbucket / GitHub それぞれの上限対応）
