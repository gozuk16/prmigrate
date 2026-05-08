package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

// Client is a Bitbucket Cloud REST API v2.0 client scoped to a single
// repository. Reuse one Client across all calls for the same repo to share
// connections, rate limiter, and retry behavior.
type Client struct {
	httpClient *http.Client
	baseURL    string // e.g. https://api.bitbucket.org/2.0/repositories/workspace/repo
	auth       Auth
	limiter    *rate.Limiter
}

// Auth carries credentials for Bitbucket. As of 2025+, Atlassian is moving
// from App Passwords to API Tokens. Both are HTTP Basic auth: username +
// (app password OR API token) as the password field.
type Auth struct {
	Username string
	Token    string // app password or API token
}

// NewClient creates a client for the specified repository (e.g. "workspace/repo").
// apiBase: e.g. "https://api.bitbucket.org/2.0" (or a test httptest server URL).
// rps is the maximum sustained request rate.
func NewClient(apiBase, repoFullName string, auth Auth, rps float64) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    fmt.Sprintf("%s/repositories/%s", apiBase, repoFullName),
		auth:       auth,
		limiter:    rate.NewLimiter(rate.Limit(rps), 1),
	}
}

// doJSON performs a GET request, parses the JSON response into out, and
// transparently retries on transient errors (5xx, 429).
//
// fullURL must be either an absolute URL (such as a paginated 'next' link)
// or a path relative to the repository base URL prefixed with "/".
func (c *Client) doJSON(ctx context.Context, fullURL string, out any) error {
	if !isAbsoluteURL(fullURL) {
		fullURL = c.baseURL + fullURL
	}

	const maxAttempts = 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(c.auth.Username, c.auth.Token)
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt == maxAttempts {
				return err
			}
			time.Sleep(backoff(attempt))
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			return json.Unmarshal(body, out)
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			if attempt == maxAttempts {
				return fmt.Errorf("bitbucket %s after %d attempts: %s", fullURL, attempt, resp.Status)
			}
			time.Sleep(backoff(attempt))
			continue
		default:
			return fmt.Errorf("bitbucket %s: %s: %s", fullURL, resp.Status, truncate(string(body), 200))
		}
	}
	return fmt.Errorf("unreachable")
}

// ListPullRequestIDs returns every PR ID in the repository, regardless of state.
// PRs are listed via /pullrequests with state filters.
func (c *Client) ListPullRequestIDs(ctx context.Context) ([]int, error) {
	// We page through every state. Bitbucket allows multiple state= params.
	// Field projection (?fields=) keeps response size small.
	params := url.Values{}
	for _, s := range []string{"OPEN", "MERGED", "DECLINED", "SUPERSEDED"} {
		params.Add("state", s)
	}
	params.Set("fields", "+values.id,+next")
	params.Set("pagelen", "50")

	startURL := c.baseURL + "/pullrequests?" + params.Encode()

	type idOnly struct {
		ID int `json:"id"`
	}
	type page struct {
		Page
		Values []idOnly `json:"values"`
	}

	var ids []int
	next := startURL
	for next != "" {
		var p page
		if err := c.doJSON(ctx, next, &p); err != nil {
			return nil, err
		}
		for _, v := range p.Values {
			ids = append(ids, v.ID)
		}
		next = p.Next
	}
	return ids, nil
}

// GetPullRequest fetches the full detail of a single PR.
func (c *Client) GetPullRequest(ctx context.Context, id int) (*PullRequest, error) {
	var pr PullRequest
	if err := c.doJSON(ctx, fmt.Sprintf("/pullrequests/%d", id), &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// ListComments returns all comments (general + inline) for a PR.
// Bitbucket returns deleted comments with deleted=true; we keep them so the
// caller can decide whether to skip.
func (c *Client) ListComments(ctx context.Context, prID int) ([]Comment, error) {
	type page struct {
		Page
		Values []Comment `json:"values"`
	}
	var all []Comment
	next := fmt.Sprintf("%s/pullrequests/%d/comments?pagelen=100", c.baseURL, prID)
	for next != "" {
		var p page
		if err := c.doJSON(ctx, next, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Values...)
		next = p.Next
	}
	return all, nil
}

// ListActivity returns the activity stream of a PR, in reverse chronological
// order (newest first), as Bitbucket emits it.
func (c *Client) ListActivity(ctx context.Context, prID int) ([]Activity, error) {
	type page struct {
		Page
		Values []Activity `json:"values"`
	}
	var all []Activity
	next := fmt.Sprintf("%s/pullrequests/%d/activity?pagelen=100", c.baseURL, prID)
	for next != "" {
		var p page
		if err := c.doJSON(ctx, next, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Values...)
		next = p.Next
	}
	return all, nil
}

// --- helpers ---

func isAbsoluteURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.IsAbs()
}

func backoff(attempt int) time.Duration {
	// 1s, 2s, 4s, 8s, 16s
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
