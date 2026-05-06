# prmigrate — Bitbucket Cloud → GitHub Pull Request Migrator

Bitbucket Cloud の Pull Request を GitHub Enterprise Cloud (または github.com) に移行するツール。

## 設計方針

このツールは外部の既存ツールのコードを参照せず、以下の公開仕様のみを参照して clean-room で実装します:

- Atlassian Bitbucket Cloud REST API v2.0 公式ドキュメント
- Jonathan Magic の Issue Import API gist (https://gist.github.com/jonmagic/5282384165e0f86ef105)
- GitHub REST API 公式ドキュメント

## 採用した戦略

### 1. Closed PR は GitHub Issue Import API で「Issue として」投入する

`POST /repos/{owner}/{repo}/import/issues` という非公開だが安定して動く API を使う。
理由:

- `created_at` / `updated_at` / `closed_at` を任意指定可能(通常の Issues/PR API では不可能)
- 通知が飛ばない (anti-abuse rate limit に引っかかりにくい)
- 1件で issue + 全 comments を一括投入できる

PR をそのまま PR として復元するのは GitHub の制約で困難 (head/base ブランチ生存が必要、
タイムスタンプ指定不可) なので、**メタデータをコメント先頭に Markdown で埋め込んだ Issue として保存** する方針。

### 2. Open PR は通常の PR API で本物の PR として再作成する

ブランチが生きていれば `POST /repos/{owner}/{repo}/pulls` で復元する。
復元できない (ブランチが削除済みなど) ものは Closed と同じく Issue として記録する。

### 3. 番号空間の整合性確保

Bitbucket は Issue と PR が別番号空間、GitHub は共通番号空間。
このツールは PR のみ移行対象だが、それでも

- Bitbucket PR #1 → GitHub Issue #1
- Bitbucket PR #2 → GitHub Issue #2

のように **番号を揃える** ことを目指す。途中の欠番には空のダミー Issue を投入する。
GitHub Issue Import API は完了順に番号を割り当てるため、**1件ずつ完了を待つ** 直列処理が必須。

### 4. 元作者・元日時はメタデータとして本文に保存する

GitHub API 制約により、Issue/Comment の作者は API トークン所有者に固定される。
本物の作者情報は body 先頭の引用ブロックに記録する:

```
> **Pull request** created by **@original-author** on 2023-04-15 09:23
> Original Bitbucket PR id: #47
> State: MERGED
> Merge commit: https://github.com/.../commit/abc123
```

### 5. ユーザーマッピング

`config.yaml` で Bitbucket nickname / UUID / Atlassian account ID → GitHub username を定義。
Bitbucket API はレスポンスにより異なる識別子を返すので、すべてのバリアントをマップする必要がある。

## ディレクトリ構成

```
prmigrate/
├── cmd/prmigrate/main.go              CLI エントリ
├── internal/
│   ├── config/                    YAML 設定の読み込み
│   ├── bitbucket/                 Bitbucket Cloud REST API クライアント
│   ├── githubimport/              GitHub Issue Import API クライアント (golden-comet)
│   ├── githubapi/                 GitHub REST API クライアント (Open PR 作成用)
│   ├── transform/                 Bitbucket PR → GitHub Issue/PR 変換
│   └── pipeline/                  オーケストレーション
└── config.example.yaml            設定ファイル雛形
```

## 使い方

```bash
# 1. 設定ファイル作成
cp config.example.yaml config.yaml
# 編集: ユーザーマッピング、リポジトリペアなど

# 2. ドライラン (Bitbucket からの取得 + 変換のみ実行、GitHub への書き込みなし)
prmigrate migrate --config config.yaml --repo myteam/myrepo --dry-run

# 3. 本実行
prmigrate migrate --config config.yaml --repo myteam/myrepo
```

## ライセンス

このツール自体の実装コードは MIT License とします。
