package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/branches/"+branch, nil)
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

// CreatePullRequest creates a GitHub pull request and returns it.
// Returns an error for non-201 responses (e.g. 422 when head == base).
func (c *Client) CreatePullRequest(ctx context.Context, prReq *CreatePullRequestRequest) (*PullRequest, error) {
	body, err := json.Marshal(prReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/pulls", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create PR failed: %s: %s", resp.Status, string(respBody))
	}

	var pr PullRequest
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &pr, nil
}

// CreateIssueComment posts a comment on a GitHub issue or pull request.
// PRs and Issues share the same comment endpoint on GitHub.
func (c *Client) CreateIssueComment(ctx context.Context, issueNumber int, commentBody string) error {
	payload := struct {
		Body string `json:"body"`
	}{Body: commentBody}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/issues/%d/comments", c.baseURL, issueNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create comment failed: %s: %s", resp.Status, string(respBody))
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "prmigrate/0.1")
}
