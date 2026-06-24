# 冪等な移行（重複 Issue/PR 防止）実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `prmigrate` を何度実行しても GitHub 側に重複 Issue/PR が作成されないようにする。

**Architecture:** 移行ループで番号 `n` を処理する前に `GET /repos/{owner}/{repo}/issues/{n}` を呼び、200 なら既存とみなしてスキップする。番号アラインメントの不変条件（Bitbucket PR #N = GitHub Issue/PR #N）を利用するため、チェック1本ですべての種別（Issue Import・GitHub PR・プレースホルダー）を統一的に扱える。

**Tech Stack:** Go 1.22+、`net/http`、`net/http/httptest`（テスト）

## Global Constraints

- パッケージ構成は既存のまま変更しない（`internal/githubapi`、`internal/pipeline`）
- テストは `httptest.NewServer` を使ったモックサーバーで書く（既存パターンに従う）
- 新しい外部ライブラリは追加しない
- `go test ./...` が全パッケージでパスすること

---

### Task 1: `githubapi.Client.IssueExists` メソッドの追加

**Files:**
- Modify: `internal/githubapi/client.go`
- Modify: `internal/githubapi/client_test.go`

**Interfaces:**
- Produces: `func (c *Client) IssueExists(ctx context.Context, issueNumber int) (bool, error)`
  - 200 → `(true, nil)`
  - 404 → `(false, nil)`
  - その他 → `(false, error)`

---

- [ ] **Step 1: 失敗するテストを書く**

`internal/githubapi/client_test.go` の末尾に3つのテストを追加する。

```go
func TestIssueExists_found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/issues/42" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"number":42,"title":"Fix bug"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	exists, err := c.IssueExists(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected issue to exist")
	}
}

func TestIssueExists_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	exists, err := c.IssueExists(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected issue to not exist")
	}
}

func TestIssueExists_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := githubapi.NewClient(srv.URL, "org/repo", "tok")
	_, err := c.IssueExists(context.Background(), 42)
	if err == nil {
		t.Error("expected error for 5xx response")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

```
go test ./internal/githubapi/... -run TestIssueExists -v
```

期待: `FAIL` — `c.IssueExists undefined`

- [ ] **Step 3: `IssueExists` を実装する**

`internal/githubapi/client.go` の `setHeaders` メソッドの直前に追加する。

```go
// IssueExists reports whether GitHub issue (or pull request) number n exists.
// A 404 response returns (false, nil); other non-200 responses return an error.
func (c *Client) IssueExists(ctx context.Context, issueNumber int) (bool, error) {
	url := fmt.Sprintf("%s/issues/%d", c.baseURL, issueNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("check issue %d: %s", issueNumber, resp.Status)
	}
}
```

- [ ] **Step 4: テストがパスすることを確認する**

```
go test ./internal/githubapi/... -v
```

期待: `PASS`（全テスト）

- [ ] **Step 5: コミット**

```bash
git add internal/githubapi/client.go internal/githubapi/client_test.go
git commit -m "feat(githubapi): add IssueExists method"
```

---

### Task 2: migrator の冪等チェックと既存テストの更新

**Files:**
- Modify: `internal/pipeline/migrator.go`
- Modify: `internal/pipeline/migrator_test.go`

**Interfaces:**
- Consumes: `githubapi.Client.IssueExists(ctx, issueNumber int) (bool, error)` — Task 1 で追加

---

- [ ] **Step 1: 既存の migrator テストを確認する**

`migrator.go` に `IssueExists` チェックを追加すると、既存テストの ghSrv ハンドラが `GET /repos/org/repo/issues/{n}` を受け取ったときに `t.Errorf` を呼ぶ（予期しないリクエストとして扱う）。そのため、実装前に既存テスト5本のハンドラを更新して 404 を返すようにする。

`internal/pipeline/migrator_test.go` の `TestMigrator_mergedPR` の ghSrv ハンドラを以下のように更新する（issueExists チェックへの 404 を追加）。

```go
ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // IssueExists check (idempotency): not yet migrated → 404
    if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/") {
        w.WriteHeader(http.StatusNotFound)
        return
    }
    if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues" {
        body, _ := io.ReadAll(r.Body)
        if err := json.Unmarshal(body, &got); err != nil {
            t.Fatalf("unmarshal request body: %v", err)
        }
        w.WriteHeader(http.StatusAccepted)
        fmt.Fprint(w, terminalImportResponse)
        return
    }
    t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
}))
```

`TestMigrator_openPR_branchExists` の ghSrv ハンドラを更新する。

```go
ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    switch {
    // IssueExists check (idempotency): not yet migrated → 404
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
        w.WriteHeader(http.StatusNotFound)
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
        rawBranch := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/branches/")
        gotBranch, _ := url.PathUnescape(rawBranch)
        if gotBranch != "feature/add" {
            t.Errorf("branch check: got %q, want feature/add", gotBranch)
        }
        w.WriteHeader(http.StatusOK)
        fmt.Fprint(w, `{"name":"feature/add"}`)
    case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/pulls":
        body, _ := io.ReadAll(r.Body)
        if err := json.Unmarshal(body, &gotPR); err != nil {
            t.Fatalf("unmarshal request body: %v", err)
        }
        w.WriteHeader(http.StatusCreated)
        fmt.Fprint(w, `{"number":1,"html_url":"http://example.com/pull/1"}`)
    default:
        t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
    }
}))
```

`TestMigrator_openPR_branchDeleted` の ghSrv ハンドラを更新する。

```go
ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    switch {
    // IssueExists check (idempotency): not yet migrated → 404
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
        w.WriteHeader(http.StatusNotFound)
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
        w.WriteHeader(http.StatusNotFound)
        fmt.Fprint(w, `{"message":"Not Found"}`)
    case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues":
        importCalled = true
        w.WriteHeader(http.StatusAccepted)
        fmt.Fprint(w, terminalImportResponse)
    default:
        t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
    }
}))
```

`TestMigrator_gapFill` の ghSrv ハンドラを更新する。

```go
ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // IssueExists check (idempotency): not yet migrated → 404
    if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/") {
        w.WriteHeader(http.StatusNotFound)
        return
    }
    if r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues" {
        importCount.Add(1)
        w.WriteHeader(http.StatusAccepted)
        fmt.Fprint(w, terminalImportResponse)
        return
    }
    t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
}))
```

`TestMigrator_dryRun` の ghSrv ハンドラを更新する。

```go
ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    switch {
    // IssueExists check is a read-only GET, allowed even in dry-run.
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
        w.WriteHeader(http.StatusNotFound)
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/org/repo/branches/"):
        rawBranch := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/branches/")
        gotBranch, _ := url.PathUnescape(rawBranch)
        if gotBranch != "feature/add" {
            t.Errorf("branch check: got %q, want feature/add", gotBranch)
        }
        w.WriteHeader(http.StatusOK)
        fmt.Fprint(w, `{"name":"feature/add"}`)
    default:
        writeAttempted = true
        t.Errorf("dry-run must not make write requests: %s %s", r.Method, r.URL.Path)
        w.WriteHeader(http.StatusInternalServerError)
    }
}))
```

- [ ] **Step 2: 新しいスキップテストを追加する**

`internal/pipeline/migrator_test.go` の末尾に追加する。

```go
// TestMigrator_skipAlreadyMigrated: when GitHub already has Issue #1,
// the migrator skips it without calling the Import API.
func TestMigrator_skipAlreadyMigrated(t *testing.T) {
	const prJSON = `{"id":1,"title":"Fix bug","state":"MERGED","created_on":"2024-01-10T09:00:00+00:00","updated_on":"2024-01-10T09:00:00+00:00"}`

	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/repo/pullrequests":
			fmt.Fprint(w, `{"values":[{"id":1}]}`)
		case "/repositories/ws/repo/pullrequests/1":
			fmt.Fprint(w, prJSON)
		case "/repositories/ws/repo/pullrequests/1/comments":
			fmt.Fprint(w, `{"values":[]}`)
		case "/repositories/ws/repo/pullrequests/1/activity":
			fmt.Fprint(w, `{"values":[]}`)
		default:
			t.Errorf("unexpected bb request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer bbSrv.Close()

	var importCalled bool
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// IssueExists check: already migrated → 200
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/issues/1":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"number":1,"title":"Fix bug"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/import/issues":
			importCalled = true
			t.Error("Import API must not be called when issue already exists")
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, terminalImportResponse)
		default:
			t.Errorf("unexpected gh request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ghSrv.Close()

	m := newTestMigrator(t, bbSrv.URL, ghSrv.URL, config.TuningConfig{BitbucketRPS: 1000})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if importCalled {
		t.Error("Import API was called despite issue already existing on GitHub")
	}
}
```

- [ ] **Step 3: テストが失敗することを確認する**

```
go test ./internal/pipeline/... -run TestMigrator_skipAlreadyMigrated -v
```

期待: `FAIL` — Import API が呼ばれることで失敗、または unexpected request エラー

- [ ] **Step 4: `migrator.go` のメインループに冪等チェックを追加する**

`internal/pipeline/migrator.go` の `Run()` メソッド内、`for n := 1; n <= maxID; n++` ループの冒頭を以下のように変更する。

変更前:
```go
	for n := 1; n <= maxID; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !idsSet[n] {
```

変更後:
```go
	for n := 1; n <= maxID; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		exists, err := m.ghapi.IssueExists(ctx, n)
		if err != nil {
			m.log.Warn("issue existence check failed, proceeding", "n", n, "err", err)
		} else if exists {
			m.log.Info("skipping: already exists on GitHub", "n", n)
			continue
		}

		if !idsSet[n] {
```

- [ ] **Step 5: 全テストがパスすることを確認する**

```
go test ./... -v
```

期待: `PASS`（全パッケージ）

- [ ] **Step 6: コミット**

```bash
git add internal/pipeline/migrator.go internal/pipeline/migrator_test.go
git commit -m "feat(pipeline): skip already-migrated issues for idempotent runs"
```
