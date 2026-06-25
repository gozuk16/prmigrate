package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Fetcher はパイプラインが Bitbucket から PR データを取得するためのインターフェース。
// *Client と *CachedClient の両方が実装する。
type Fetcher interface {
	ListPullRequestIDs(ctx context.Context) ([]int, error)
	GetPullRequest(ctx context.Context, id int) (*PullRequest, error)
	ListComments(ctx context.Context, prID int) ([]Comment, error)
	ListActivity(ctx context.Context, prID int) ([]Activity, error)
}

// コンパイル時にインターフェースの実装を検証する。
var _ Fetcher = (*Client)(nil)
var _ Fetcher = (*CachedClient)(nil)

// cachedBundle は1つの PR に関するすべてのデータをまとめたキャッシュ単位。
type cachedBundle struct {
	PR       PullRequest `json:"pr"`
	Comments []Comment   `json:"comments"`
	Activity []Activity  `json:"activity"`
}

// CachedClient は *Client をラップし、終端状態（MERGED/DECLINED/SUPERSEDED）の
// PR をローカルファイルにキャッシュする。
type CachedClient struct {
	inner    *Client
	cacheDir string
	loaded   map[int]*cachedBundle
}

// NewCachedClient は CachedClient を作成する。cacheDir が存在しない場合は作成する。
// ディレクトリの作成に失敗した場合はエラーを返す。
func NewCachedClient(inner *Client, cacheDir string) (*CachedClient, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", cacheDir, err)
	}
	return &CachedClient{
		inner:    inner,
		cacheDir: cacheDir,
		loaded:   make(map[int]*cachedBundle),
	}, nil
}

// isTerminalState は PR がキャッシュ対象の終端状態かを返す。
func isTerminalState(state string) bool {
	return state == "MERGED" || state == "DECLINED" || state == "SUPERSEDED"
}

func (c *CachedClient) cachePath(id int) string {
	return filepath.Join(c.cacheDir, fmt.Sprintf("%d.json", id))
}

func (c *CachedClient) loadFromFile(id int) (*cachedBundle, error) {
	data, err := os.ReadFile(c.cachePath(id))
	if err != nil {
		return nil, err
	}
	var b cachedBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *CachedClient) saveToFile(id int, b *cachedBundle) error {
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("marshal cache for PR %d: %w", id, err)
	}
	if err := os.WriteFile(c.cachePath(id), data, 0o644); err != nil {
		return fmt.Errorf("write cache for PR %d: %w", id, err)
	}
	return nil
}

// ListPullRequestIDs は常に inner を呼ぶ（キャッシュしない）。
func (c *CachedClient) ListPullRequestIDs(ctx context.Context) ([]int, error) {
	return c.inner.ListPullRequestIDs(ctx)
}

// GetPullRequest はキャッシュファイルがあればそこから返す。なければ API から取得し、
// 終端状態であればコメント・アクティビティも取得してキャッシュに保存する。
// キャッシュファイルの読み込みに失敗した場合（壊れた JSON 等）は API から再取得する。
func (c *CachedClient) GetPullRequest(ctx context.Context, id int) (*PullRequest, error) {
	if b, err := c.loadFromFile(id); err == nil {
		c.loaded[id] = b
		return &b.PR, nil
	}

	pr, err := c.inner.GetPullRequest(ctx, id)
	if err != nil {
		return nil, err
	}

	if isTerminalState(pr.State) {
		comments, err := c.inner.ListComments(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("fetch comments for cache PR %d: %w", id, err)
		}
		activity, err := c.inner.ListActivity(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("fetch activity for cache PR %d: %w", id, err)
		}
		b := &cachedBundle{PR: *pr, Comments: comments, Activity: activity}
		if err := c.saveToFile(id, b); err != nil {
			return nil, err
		}
		c.loaded[id] = b
	}
	return pr, nil
}

// ListComments は loaded map にエントリがあればキャッシュから返す。なければ API を呼ぶ。
func (c *CachedClient) ListComments(ctx context.Context, prID int) ([]Comment, error) {
	if b, ok := c.loaded[prID]; ok {
		return b.Comments, nil
	}
	return c.inner.ListComments(ctx, prID)
}

// ListActivity は loaded map にエントリがあればキャッシュから返す。なければ API を呼ぶ。
func (c *CachedClient) ListActivity(ctx context.Context, prID int) ([]Activity, error) {
	if b, ok := c.loaded[prID]; ok {
		return b.Activity, nil
	}
	return c.inner.ListActivity(ctx, prID)
}
