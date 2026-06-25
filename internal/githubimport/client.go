package githubimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AcceptHeader is the required Accept header to access the import API.
// It is a media type version negotiator -- without it the API returns 404.
const AcceptHeader = "application/vnd.github.golden-comet-preview+json"

// MaxRequestBytes is the documented upper bound on a single import request
// body. Combined comment bodies must fit within this.
const MaxRequestBytes = 1 << 20 // 1 MiB

// Client targets a single GitHub repository's import endpoint.
type Client struct {
	httpClient *http.Client
	baseURL    string // .../repos/{owner}/{repo}
	token      string
}

// NewClient creates a client for the specified repository.
//   - apiBase: e.g. "https://api.github.com" for github.com,
//     "https://github.example.com/api/v3" for GitHub Enterprise Server.
//     For GitHub Enterprise Cloud (data residency) it is the same as
//     github.com from a hostname perspective, just different repo URL.
//   - repoFullName: "owner/repo"
//   - token: a fine-grained or classic PAT with admin:repo on the target.
func NewClient(apiBase, repoFullName, token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    fmt.Sprintf("%s/repos/%s", apiBase, repoFullName),
		token:      token,
	}
}

// Submit posts a single import request and returns the initial pending status.
// Use Wait to block until the import reaches a terminal state.
func (c *Client) Submit(ctx context.Context, req *ImportRequest) (*ImportStatus, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if len(body) > MaxRequestBytes {
		return nil, fmt.Errorf("request body %d bytes exceeds 1MiB limit", len(body))
	}

	httpReq, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		c.baseURL+"/import/issues",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("import submit failed: %s: %s",
			resp.Status, string(respBody))
	}

	var status ImportStatus
	if err := json.Unmarshal(respBody, &status); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(respBody))
	}
	return &status, nil
}

// PollStatus fetches the current status of an in-flight or completed import
// using its status URL.
func (c *Client) PollStatus(ctx context.Context, statusURL string) (*ImportStatus, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll failed: %s: %s", resp.Status, string(respBody))
	}

	var status ImportStatus
	if err := json.Unmarshal(respBody, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// SubmitAndWait submits an import and blocks (with backoff polling) until it
// reaches a terminal state, or the context is cancelled.
//
// Polling intervals start at 1s and double up to 8s, then stay at 8s.
// Most imports complete in 1-3s.
func (c *Client) SubmitAndWait(ctx context.Context, req *ImportRequest) (*ImportStatus, error) {
	initial, err := c.Submit(ctx, req)
	if err != nil {
		return nil, err
	}
	if initial.IsTerminal() {
		return initial, nil
	}

	wait := 1 * time.Second
	const maxWait = 8 * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		status, err := c.PollStatus(ctx, initial.URL)
		if err != nil {
			return nil, err
		}
		if status.IsTerminal() {
			return status, nil
		}
		if wait < maxWait {
			wait *= 2
			if wait > maxWait {
				wait = maxWait
			}
		}
	}
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", AcceptHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "prmigrate/0.1")
}
