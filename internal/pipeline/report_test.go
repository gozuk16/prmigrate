package pipeline_test

import (
	"testing"

	"github.com/gozuk16/prmigrate/internal/pipeline"
)

func TestDryRunReport_CountByAction(t *testing.T) {
	r := pipeline.DryRunReport{
		Entries: []pipeline.DryRunEntry{
			{Action: pipeline.ActionGitHubPR},
			{Action: pipeline.ActionIssueImport},
			{Action: pipeline.ActionIssueImport},
			{Action: pipeline.ActionPlaceholder},
		},
	}
	if got := r.CountByAction(pipeline.ActionGitHubPR); got != 1 {
		t.Errorf("CountByAction(github-pr) = %d, want 1", got)
	}
	if got := r.CountByAction(pipeline.ActionIssueImport); got != 2 {
		t.Errorf("CountByAction(issue-import) = %d, want 2", got)
	}
	if got := r.CountByAction(pipeline.ActionPlaceholder); got != 1 {
		t.Errorf("CountByAction(placeholder) = %d, want 1", got)
	}
}

func TestDryRunReport_Total(t *testing.T) {
	r := pipeline.DryRunReport{
		Entries: []pipeline.DryRunEntry{
			{Action: pipeline.ActionGitHubPR},
			{Action: pipeline.ActionIssueImport},
		},
	}
	if got := r.Total(); got != 2 {
		t.Errorf("Total() = %d, want 2", got)
	}
}

func TestDryRunReport_Empty(t *testing.T) {
	var r pipeline.DryRunReport
	if got := r.Total(); got != 0 {
		t.Errorf("Total() on empty = %d, want 0", got)
	}
	if got := r.CountByAction(pipeline.ActionGitHubPR); got != 0 {
		t.Errorf("CountByAction on empty = %d, want 0", got)
	}
}
