# CLI 引数による Bitbucket/GitHub リポジトリ1対1指定 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `-repo bb/repo -gh-repo org/repo` の引数ペアだけで `repo_mapping` なしに移行を実行できるようにする。

**Architecture:** 2つの変更から成る。(1) `config.Validate()` から `repo_mapping` の必須チェックを外す。(2) `main.go` に `-gh-repo` フラグを追加し、ターゲット決定ロジックを更新する。Task 1 が Task 2 の前提。

**Tech Stack:** Go 1.22+、`flag` 標準ライブラリ、`github.com/BurntSushi/toml`

## Global Constraints

- 新しい外部ライブラリは追加しない
- 既存の `-repo`・`-all`・`-dry-run` フラグの動作は変えない（後方互換）
- エラーメッセージは stderr に出力（`fail()` 関数経由）
- `go test ./...` が全パスすること

---

### Task 1: `config.Validate()` の `repo_mapping` 必須チェックを削除する

`-repo -gh-repo` 組み合わせでは `repo_mapping` がなくても動作させたい。現在 `config.Validate()` は `len(c.RepoMapping) == 0` でエラーを返すため、この条件を削除する。エントリが存在する場合のフォーマット検証（`workspace/repo` 形式）は残す。

**Files:**
- Modify: `internal/config/config.go:98-110`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `func (c *Config) Validate() error` — `repo_mapping` が空でもエラーを返さない

---

- [ ] **Step 1: 失敗するテストを書く**

`internal/config/config_test.go` の末尾に追加する:

```go
func TestValidate_emptyRepoMapping_ok(t *testing.T) {
	cfg := &config.Config{
		Bitbucket:   config.BitbucketConfig{Username: "user"},
		RepoMapping: map[string]string{},
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error with empty repo_mapping, got: %v", err)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

```
go test ./internal/config/... -run TestValidate_emptyRepoMapping_ok -v
```

期待: `FAIL` — `"repo_mapping must contain at least one entry"`

- [ ] **Step 3: `repo_mapping` 必須チェックを削除する**

`internal/config/config.go` の `Validate()` を以下のように変更する:

変更前:
```go
func (c *Config) Validate() error {
	if c.Bitbucket.Username == "" {
		return fmt.Errorf("bitbucket.username is required")
	}
	if len(c.RepoMapping) == 0 {
		return fmt.Errorf("repo_mapping must contain at least one entry")
	}
	for bRepo, gRepo := range c.RepoMapping {
		if !strings.Contains(bRepo, "/") || !strings.Contains(gRepo, "/") {
			return fmt.Errorf(`repo_mapping entries must be in "workspace/repo" form: %q -> %q`, bRepo, gRepo)
		}
	}
	return nil
}
```

変更後:
```go
func (c *Config) Validate() error {
	if c.Bitbucket.Username == "" {
		return fmt.Errorf("bitbucket.username is required")
	}
	for bRepo, gRepo := range c.RepoMapping {
		if !strings.Contains(bRepo, "/") || !strings.Contains(gRepo, "/") {
			return fmt.Errorf(`repo_mapping entries must be in "workspace/repo" form: %q -> %q`, bRepo, gRepo)
		}
	}
	return nil
}
```

- [ ] **Step 4: 全テストがパスすることを確認する**

```
go test ./internal/config/... -v
```

期待: `PASS`（全テスト）

- [ ] **Step 5: コミット**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): make repo_mapping optional in Validate"
```

---

### Task 2: `-gh-repo` フラグの追加とターゲット決定ロジックの更新

**Files:**
- Modify: `cmd/prmigrate/main.go`

**Interfaces:**
- Consumes: `config.Config.RepoMapping`、`config.Config.LookupRepo` — 変更なし（Task 1 とは独立）

---

- [ ] **Step 1: `main.go` を変更する**

`cmd/prmigrate/main.go` のフラグ定義とターゲット決定ロジックを以下のように変更する。

フラグ定義（`flag.Parse()` の前）を変更前から:
```go
var (
    configPath = flag.String("config", "config.toml", "path to YAML config file")
    repo       = flag.String("repo", "", `Bitbucket repo to migrate, e.g. "workspace/myrepo"`)
    all        = flag.Bool("all", false, "migrate every repo in repo_mapping")
    dryRun     = flag.Bool("dry-run", false, "do not write to GitHub; only fetch and transform")
    verbose    = flag.Bool("v", false, "verbose logging")
)
```

変更後:
```go
var (
    configPath = flag.String("config", "config.toml", "path to YAML config file")
    repo       = flag.String("repo", "", `Bitbucket repo to migrate, e.g. "workspace/myrepo"`)
    ghRepo     = flag.String("gh-repo", "", `GitHub repo to migrate into, e.g. "org/repo" (overrides repo_mapping when used with -repo)`)
    all        = flag.Bool("all", false, "migrate every repo in repo_mapping")
    dryRun     = flag.Bool("dry-run", false, "do not write to GitHub; only fetch and transform")
    verbose    = flag.Bool("v", false, "verbose logging")
)
```

ターゲット決定ロジック（`// Decide target set.` の直前から `default:` ブロックの末尾まで）を変更前から:
```go
// Decide target set.
var targets [][2]string // {bb, gh} pairs
switch {
case *all:
    for bb, gh := range cfg.RepoMapping {
        targets = append(targets, [2]string{bb, gh})
    }
case *repo != "":
    gh, ok := cfg.LookupRepo(*repo)
    if !ok {
        fail(log, "repo lookup", fmt.Errorf("%q is not in repo_mapping", *repo))
    }
    targets = [][2]string{{*repo, gh}}
default:
    fmt.Fprintln(os.Stderr, "either -repo or -all is required")
    flag.Usage()
    os.Exit(2)
}
```

変更後:
```go
// Validate flag combinations.
if *ghRepo != "" && *repo == "" {
    fail(log, "flag validation", fmt.Errorf("-gh-repo requires -repo"))
}
if *ghRepo != "" && *all {
    fail(log, "flag validation", fmt.Errorf("-gh-repo cannot be used with -all"))
}

// Decide target set.
var targets [][2]string // {bb, gh} pairs
switch {
case *all:
    for bb, gh := range cfg.RepoMapping {
        targets = append(targets, [2]string{bb, gh})
    }
case *repo != "" && *ghRepo != "":
    targets = [][2]string{{*repo, *ghRepo}}
case *repo != "":
    gh, ok := cfg.LookupRepo(*repo)
    if !ok {
        fail(log, "repo lookup", fmt.Errorf("%q is not in repo_mapping", *repo))
    }
    targets = [][2]string{{*repo, gh}}
default:
    fmt.Fprintln(os.Stderr, "either -repo or -all is required")
    flag.Usage()
    os.Exit(2)
}
```

- [ ] **Step 2: ビルドが通ることを確認する**

```
go build ./cmd/prmigrate/
```

期待: エラーなし（バイナリが生成される）

- [ ] **Step 3: ヘルプ出力に `-gh-repo` が表示されることを確認する**

```
./prmigrate -help 2>&1 | grep gh-repo
```

期待: `-gh-repo string` を含む行が出力される

- [ ] **Step 4: エラーケースを確認する**

```bash
# -gh-repo のみ（-repo なし）→ エラー
./prmigrate -config config.toml -gh-repo org/repo 2>&1 | grep "gh-repo requires"

# -gh-repo + -all → エラー
./prmigrate -config config.toml -all -gh-repo org/repo 2>&1 | grep "cannot be used"
```

期待:
- 1行目: `msg="-gh-repo requires -repo"` を含むエラーログ
- 2行目: `msg="-gh-repo cannot be used with -all"` を含むエラーログ

注: このステップは config.toml が存在しない場合でも `-config` のロードで先に失敗する。実際のエラーチェックには有効な config.toml（認証情報のみ、`repo_mapping` なし）が必要。`config.toml` が手元にある場合は実行すること。

- [ ] **Step 5: 全テストがパスすることを確認する**

```
go test ./...
```

期待: `PASS`（全パッケージ）

- [ ] **Step 6: コミット**

```bash
git add cmd/prmigrate/main.go
git commit -m "feat(cli): add -gh-repo flag for direct repo pair specification"
```
