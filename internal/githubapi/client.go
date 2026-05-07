package githubapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client targets a single GitHub repository's REST API endpoints.
type Client struct {
	httpClient *http.Client
	baseURL    string // .../repos/{owner}/{repo}
	token      string
}

// NewClient creates a client for the specified repository.
//   - apiBase: e.g. "https://api.github.com"
//   - repoFullName: "owner/repo"
//   - token: a PAT or fine-grained token with contents:read and pull-requests:write scope.
func NewClient(apiBase, repoFullName, token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    fmt.Sprintf("%s/repos/%s", apiBase, repoFullName),
		token:      token,
	}
}

// BranchExists reports whether the named branch exists in the repository.
// A 404 response returns (false, nil); other non-200 responses return an error.
func (c *Client) BranchExists(ctx context.Context, branch string) (bool, error) {
	url := fmt.Sprintf("%s/branches/%s", c.baseURL, branch)
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
		return false, fmt.Errorf("check branch %q: %s", branch, resp.Status)
	}
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "prmigrate/0.1")
}
