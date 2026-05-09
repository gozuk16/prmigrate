package pipeline

// DryRunAction describes what would happen to a single Bitbucket PR.
type DryRunAction string

const (
	ActionGitHubPR    DryRunAction = "github-pr"
	ActionIssueImport DryRunAction = "issue-import"
	ActionPlaceholder DryRunAction = "placeholder"
)

// DryRunEntry records the planned action for one PR.
type DryRunEntry struct {
	PRNumber     int
	Title        string
	Action       DryRunAction
	State        string // "OPEN" / "MERGED" / etc. (empty for placeholder)
	Head         string // ActionGitHubPR only: source branch
	Base         string // ActionGitHubPR only: destination branch
	CommentCount int
	Body         string // transformed Markdown body
}

// DryRunReport collects planned actions after a dry-run Run().
type DryRunReport struct {
	Entries []DryRunEntry
}

// CountByAction returns the number of entries with the given action.
func (r *DryRunReport) CountByAction(a DryRunAction) int {
	n := 0
	for _, e := range r.Entries {
		if e.Action == a {
			n++
		}
	}
	return n
}

// Total returns the total number of entries.
func (r *DryRunReport) Total() int {
	return len(r.Entries)
}
