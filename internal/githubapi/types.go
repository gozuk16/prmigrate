// Package githubapi implements a client for the GitHub REST API v3.
// This package handles branch existence checks and pull request creation,
// complementing the githubimport package which uses the Issue Import API.
package githubapi

// CreatePullRequestRequest is the JSON body for POST /repos/{owner}/{repo}/pulls.
type CreatePullRequestRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"` // source branch name
	Base  string `json:"base"` // destination branch name
}

// PullRequest is the relevant subset of GitHub's pull request response.
type PullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}
