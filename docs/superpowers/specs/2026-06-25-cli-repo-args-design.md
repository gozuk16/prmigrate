# 設計書: CLI 引数による Bitbucket/GitHub リポジトリの1対1指定

**作成日**: 2026-06-25  
**ステータス**: 承認済み

## 背景と課題

現在 `-repo` フラグは Bitbucket リポジトリ名を受け取るが、対応する GitHub リポジトリは必ず config.toml の `repo_mapping` から引く必要がある。スクリプトから特定のペアを1回限り実行したい場合でも、あらかじめ `repo_mapping` にエントリを追加しなければならない。

## 目標

`-gh-repo` フラグを追加し、Bitbucket と GitHub のリポジトリペアをコマンドライン引数だけで指定できるようにする。

```bash
prmigrate -repo workspace/myrepo -gh-repo org/repo
prmigrate -repo workspace/myrepo -gh-repo org/repo -dry-run
```

## 設計

### CLI 変更（`cmd/prmigrate/main.go`）

`-gh-repo` フラグを追加する。

```
-gh-repo string   GitHub repo to migrate into, e.g. "org/repo" (overrides repo_mapping when used with -repo)
```

### ターゲット決定ロジック

| フラグ組み合わせ | 動作 |
|---|---|
| `-repo` のみ | 従来通り `repo_mapping` から GitHub リポジトリを引く |
| `-repo` + `-gh-repo` | `repo_mapping` を参照せず引数のペアを直接使う |
| `-all` | 従来通り `repo_mapping` の全エントリ |
| `-gh-repo` のみ | エラー終了: `"-gh-repo requires -repo"` |
| `-gh-repo` + `-all` | エラー終了: `"-gh-repo cannot be used with -all"` |

### バリデーション

- `-gh-repo` が指定されているが `-repo` がない → `"-gh-repo requires -repo"` でエラー
- `-gh-repo` と `-all` が同時指定 → `"-gh-repo cannot be used with -all"` でエラー
- `-repo` + `-gh-repo` のとき、`repo_mapping` に該当エントリがなくてもエラーにしない（引数を優先）
- `repo_mapping` セクション自体が config.toml に存在しなくてもエラーにしない（認証情報のみで動作）

### 変更ファイル

| ファイル | 変更内容 |
|---|---|
| `cmd/prmigrate/main.go` | `-gh-repo` フラグ追加・バリデーション追加・ターゲット決定ロジック更新 |

テストはCLI統合テストがないため不要。バリデーションのロジック変更のみで、`pipeline` パッケージへの変更はなし。

## 使用例

```bash
# 1対1でスクリプトから実行
prmigrate -config config.toml -repo workspace/myrepo -gh-repo org/repo

# ドライランで確認
prmigrate -config config.toml -repo workspace/myrepo -gh-repo org/repo -dry-run

# 従来通り repo_mapping から引く（後方互換）
prmigrate -config config.toml -repo workspace/myrepo
prmigrate -config config.toml -all
```
