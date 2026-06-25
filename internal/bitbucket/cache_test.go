package bitbucket_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gozuk16/prmigrate/internal/bitbucket"
)

// bbHandler は Bitbucket テストサーバーのハンドラを返す。
// apiCalls はリクエスト数をカウントするためのアトミックカウンタ。
func bbHandler(t *testing.T, apiCalls *int32, prID int, prJSON, commentsJSON, activityJSON string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(apiCalls, 1)
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, fmt.Sprintf("/pullrequests/%d/comments", prID)):
			_, _ = fmt.Fprint(w, commentsJSON)
		case strings.HasSuffix(p, fmt.Sprintf("/pullrequests/%d/activity", prID)):
			_, _ = fmt.Fprint(w, activityJSON)
		case strings.HasSuffix(p, fmt.Sprintf("/pullrequests/%d", prID)):
			_, _ = fmt.Fprint(w, prJSON)
		default:
			http.NotFound(w, r)
		}
	}
}

const (
	mergedPRJSON  = `{"id":1,"title":"Fix","state":"MERGED","created_on":"2024-01-01T00:00:00+00:00","updated_on":"2024-01-01T00:00:00+00:00"}`
	openPRJSON    = `{"id":1,"title":"WIP","state":"OPEN","created_on":"2024-01-01T00:00:00+00:00","updated_on":"2024-01-01T00:00:00+00:00"}`
	emptyListJSON = `{"values":[]}`
)

// TestCachedClient_terminalPR_cachedAfterFirstCall は、クローズ済み PR が
// 最初の呼び出し後にキャッシュされ、2回目以降は API を呼ばないことを確認する。
func TestCachedClient_terminalPR_cachedAfterFirstCall(t *testing.T) {
	var apiCalls int32
	srv := httptest.NewServer(bbHandler(t, &apiCalls, 1, mergedPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	// 1回目: API 呼び出し（PR + comments + activity = 3回）
	pr, err := c.GetPullRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.ID != 1 {
		t.Errorf("expected PR ID 1, got %d", pr.ID)
	}
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected 3 API calls after first GetPullRequest, got %d", got)
	}

	// ListComments / ListActivity は loaded map から返るため API 呼び出しなし
	if _, err := c.ListComments(ctx, 1); err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if _, err := c.ListActivity(ctx, 1); err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected still 3 API calls after ListComments/Activity, got %d", got)
	}

	// 2回目: 新しい CachedClient（再起動を模擬）→ キャッシュから読む
	c2, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient (2nd): %v", err)
	}
	pr2, err := c2.GetPullRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPullRequest (2nd): %v", err)
	}
	if pr2.ID != 1 {
		t.Errorf("expected PR ID 1 from cache, got %d", pr2.ID)
	}
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected no new API calls on cache hit, got %d total", got)
	}

	// キャッシュファイルが存在することを確認
	if _, err := os.Stat(filepath.Join(cacheDir, "1.json")); os.IsNotExist(err) {
		t.Error("expected cache file to exist")
	}
}

// TestCachedClient_openPR_notCached は OPEN PR がキャッシュされないことを確認する。
func TestCachedClient_openPR_notCached(t *testing.T) {
	var apiCalls int32
	srv := httptest.NewServer(bbHandler(t, &apiCalls, 1, openPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	if _, err := c.GetPullRequest(ctx, 1); err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	// OPEN PR: API 1回のみ（comments/activity は GetPullRequest 内では取得しない）
	if got := atomic.LoadInt32(&apiCalls); got != 1 {
		t.Errorf("expected 1 API call for OPEN PR, got %d", got)
	}
	// キャッシュファイルが存在しないことを確認
	if _, err := os.Stat(filepath.Join(cacheDir, "1.json")); !os.IsNotExist(err) {
		t.Error("expected no cache file for OPEN PR")
	}
}

// TestCachedClient_corruptCache_fallsBackToAPI は壊れた JSON キャッシュの場合に
// API から再取得することを確認する。
func TestCachedClient_corruptCache_fallsBackToAPI(t *testing.T) {
	var apiCalls int32
	srv := httptest.NewServer(bbHandler(t, &apiCalls, 1, mergedPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	// 壊れたキャッシュファイルを事前に配置
	if err := os.WriteFile(filepath.Join(cacheDir, "1.json"), []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	pr, err := c.GetPullRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.ID != 1 {
		t.Errorf("expected PR ID 1, got %d", pr.ID)
	}
	// フォールバック: PR + comments + activity = 3回
	if got := atomic.LoadInt32(&apiCalls); got != 3 {
		t.Errorf("expected 3 API calls on corrupt cache fallback, got %d", got)
	}

	// キャッシュが修復されていることを確認
	data, err := os.ReadFile(filepath.Join(cacheDir, "1.json"))
	if err != nil {
		t.Fatalf("expected repaired cache file: %v", err)
	}
	var repaired struct {
		PR struct {
			ID int `json:"id"`
		} `json:"pr"`
	}
	if err := json.Unmarshal(data, &repaired); err != nil {
		t.Fatalf("repaired cache is not valid JSON: %v", err)
	}
	if repaired.PR.ID != 1 {
		t.Errorf("repaired cache has wrong PR ID: got %d, want 1", repaired.PR.ID)
	}
}

// TestCachedClient_writeError_returnsError はキャッシュ書き込みに失敗した場合に
// エラーが返ることを確認する。
func TestCachedClient_writeError_returnsError(t *testing.T) {
	srv := httptest.NewServer(bbHandler(t, new(int32), 1, mergedPRJSON, emptyListJSON, emptyListJSON))
	defer srv.Close()

	cacheDir := t.TempDir()
	// "1.json" をディレクトリとして作成し、同名ファイルへの書き込みを失敗させる
	if err := os.Mkdir(filepath.Join(cacheDir, "1.json"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	inner := bitbucket.NewClient(srv.URL, "ws/repo", bitbucket.Auth{Username: "u", Token: "t"}, 1000)
	c, err := bitbucket.NewCachedClient(inner, cacheDir)
	if err != nil {
		t.Fatalf("NewCachedClient: %v", err)
	}
	ctx := context.Background()

	_, err = c.GetPullRequest(ctx, 1)
	if err == nil {
		t.Error("expected error when cache write fails, got nil")
	}
}
