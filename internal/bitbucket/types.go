// Package bitbucket defines types and a client for the Bitbucket Cloud REST API v2.0.
//
// References:
//   - https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/
//   - https://developer.atlassian.com/cloud/bitbucket/rest/intro/
package bitbucket

import "time"

// PullRequest represents a Bitbucket Cloud pull request.
//
// Field naming follows the Bitbucket API JSON schema. Only fields we actually
// use during migration are decoded; many optional fields are intentionally omitted.
type PullRequest struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"` // OPEN | MERGED | DECLINED | SUPERSEDED
	CreatedOn   time.Time `json:"created_on"`
	UpdatedOn   time.Time `json:"updated_on"`

	Author       *User       `json:"author"`
	Reviewers    []User      `json:"reviewers"`
	Participants []Participant `json:"participants"`

	Source      Endpoint `json:"source"`
	Destination Endpoint `json:"destination"`

	MergeCommit *Commit `json:"merge_commit"`

	Links Links `json:"links"`
}

// Endpoint is the source or destination of a PR.
// Note: source.repository may be nil if the source repo (a fork) was deleted.
type Endpoint struct {
	Branch     *Branch     `json:"branch"`
	Commit     *Commit     `json:"commit"`
	Repository *Repository `json:"repository"`
}

type Branch struct {
	Name string `json:"name"`
}

type Commit struct {
	Hash  string `json:"hash"`
	Links Links  `json:"links"`
}

type Repository struct {
	FullName string `json:"full_name"` // "workspace/repo-slug"
	UUID     string `json:"uuid"`
}

// User represents a Bitbucket user. Bitbucket may return any of nickname,
// UUID, or Atlassian account_id depending on context, so we capture all.
type User struct {
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
	UUID        string `json:"uuid"`
	AccountID   string `json:"account_id"`
	Type        string `json:"type"`
}

// Identifiers returns all known identifiers for the user, useful for
// matching against the user mapping table.
func (u *User) Identifiers() []string {
	if u == nil {
		return nil
	}
	ids := make([]string, 0, 3)
	if u.Nickname != "" {
		ids = append(ids, u.Nickname)
	}
	if u.UUID != "" {
		ids = append(ids, u.UUID)
	}
	if u.AccountID != "" {
		ids = append(ids, u.AccountID)
	}
	return ids
}

type Participant struct {
	User      *User      `json:"user"`
	Role      string     `json:"role"` // PARTICIPANT | REVIEWER
	Approved  bool       `json:"approved"`
	State     string     `json:"state"` // approved | changes_requested | nil
	ParticipatedOn *time.Time `json:"participated_on"`
}

type Links struct {
	Self    Link `json:"self"`
	HTML    Link `json:"html"`
	Comments Link `json:"comments"`
	Activity Link `json:"activity"`
}

type Link struct {
	Href string `json:"href"`
	Name string `json:"name"`
}

// Comment represents either a general PR comment or an inline (review) comment.
// Inline comments have the Inline field populated.
type Comment struct {
	ID        int       `json:"id"`
	CreatedOn time.Time `json:"created_on"`
	UpdatedOn time.Time `json:"updated_on"`
	Content   Content   `json:"content"`
	User      *User     `json:"user"`
	Deleted   bool      `json:"deleted"`
	Parent    *Comment  `json:"parent"`
	Inline    *Inline   `json:"inline"`
	Links     Links     `json:"links"`
}

// Content is Bitbucket's representation of formatted text. We use the raw form.
type Content struct {
	Raw    string `json:"raw"`
	Markup string `json:"markup"` // typically "markdown"
	HTML   string `json:"html"`
}

// Inline indicates an inline (review) comment's location.
// Path is the file path. From/To indicate line numbers (only one is set
// when the comment is on a single line; both nil means file-level comment).
type Inline struct {
	Path     string `json:"path"`
	From     *int   `json:"from"`
	To       *int   `json:"to"`
	Outdated bool   `json:"outdated"`
}

// IsInline reports whether this is a review (inline) comment.
func (c *Comment) IsInline() bool {
	return c.Inline != nil
}

// Activity is one entry in the activity stream of a PR. Each entry is a
// discriminated union: exactly one of Approval / Update / Comment is populated.
type Activity struct {
	Approval *Approval `json:"approval,omitempty"`
	Update   *Update   `json:"update,omitempty"`
	Comment  *Comment  `json:"comment,omitempty"`
}

type Approval struct {
	Date time.Time `json:"date"`
	User *User     `json:"user"`
}

type Update struct {
	Date   time.Time `json:"date"`
	State  string    `json:"state"`
	Author *User     `json:"author"`
	Title  string    `json:"title"`
	// ... reason, description before/after etc. (omitted)
}

// Page is the generic envelope for paginated Bitbucket responses.
// We decode raw RawValues so each caller can unmarshal into its own concrete type.
type Page struct {
	Size      int    `json:"size"`
	Page      int    `json:"page"`
	Pagelen   int    `json:"pagelen"`
	Next      string `json:"next"`     // empty when no next page
	Previous  string `json:"previous"` // empty when first page
}
