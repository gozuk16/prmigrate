// Package githubimport implements a client for the GitHub Issue Import API
// (also known as "golden-comet"). This is an unofficial but stable API
// originally created for the Google Code shutdown migration in 2015.
//
// Reference: https://gist.github.com/jonmagic/5282384165e0f86ef105
//
// Key properties of this API:
//   - Allows arbitrary created_at, updated_at, closed_at on issues.
//   - Comments may also have created_at.
//   - Imports are processed asynchronously: POST returns a status URL to poll.
//   - Issue numbers are assigned in completion order, NOT submission order.
//     If number ordering matters (it does for us), submit and wait serially.
//   - Does NOT trigger notifications -- safer than the regular Issues API
//     for bulk import.
//   - Requires admin permission on the target repository.
//   - Maximum request body size: 1MB.
//   - Comment authors cannot be specified; they are always the token owner.
//     We embed original author info in the comment body itself.
package githubimport

import "time"

// ImportRequest is the JSON body for POST /repos/{owner}/{repo}/import/issues.
type ImportRequest struct {
	Issue    Issue     `json:"issue"`
	Comments []Comment `json:"comments,omitempty"`
}

// Issue is the issue portion of an import request.
//
// Per the spec:
//   - Title and Body are required.
//   - CreatedAt, UpdatedAt, ClosedAt are optional but the whole point of
//     using this API is that they CAN be set, unlike the standard Issues API.
//   - Closed defaults to false. Setting Closed=true with no ClosedAt was
//     historically broken; always set ClosedAt when Closed=true.
//   - Assignee must be a real GitHub user with access to the target repo,
//     otherwise the entire import fails. Leave empty when uncertain.
//   - Labels must already exist on the target repo OR the import fails;
//     create labels up-front via the regular API.
type Issue struct {
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	Assignee  string     `json:"assignee,omitempty"`
	Milestone *int       `json:"milestone,omitempty"`
	Closed    bool       `json:"closed,omitempty"`
	Labels    []string   `json:"labels,omitempty"`
}

// Comment is one comment in an import request. Body is required;
// CreatedAt is optional but should be set to preserve chronology.
type Comment struct {
	Body      string     `json:"body"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// ImportStatus is the response body from POST and from polling GET.
type ImportStatus struct {
	ID               int64           `json:"id"`
	Status           string          `json:"status"` // "pending" | "imported" | "failed" | "error"
	URL              string          `json:"url"`
	ImportIssuesURL  string          `json:"import_issues_url"`
	RepositoryURL    string          `json:"repository_url"`
	IssueURL         string          `json:"issue_url,omitempty"` // populated once imported
	CreatedAt        *time.Time      `json:"created_at,omitempty"`
	UpdatedAt        *time.Time      `json:"updated_at,omitempty"`
	Errors           []ImportError   `json:"errors,omitempty"`
}

// ImportError describes one validation or processing error returned by the API.
type ImportError struct {
	Location string `json:"location"`
	Resource string `json:"resource"`
	Field    string `json:"field"`
	Value    string `json:"value"`
	Code     string `json:"code"`
}

// IsTerminal reports whether the import has reached a final state.
func (s *ImportStatus) IsTerminal() bool {
	return s.Status == "imported" || s.Status == "failed" || s.Status == "error"
}

// IssueNumber extracts the assigned issue number from IssueURL.
// Returns 0 if not yet assigned. Format: ".../repos/{owner}/{repo}/issues/{n}"
func (s *ImportStatus) IssueNumber() int {
	if s.IssueURL == "" {
		return 0
	}
	return parseTrailingInt(s.IssueURL)
}

func parseTrailingInt(s string) int {
	n := 0
	mul := 1
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n += int(c-'0') * mul
		mul *= 10
	}
	return n
}
