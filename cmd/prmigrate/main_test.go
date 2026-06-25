package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gozuk16/prmigrate/internal/pipeline"
)

func TestPrintDryRunReport_summaryHeader(t *testing.T) {
	var buf bytes.Buffer
	printDryRunReport(&buf, "ws/repo", "org/repo", pipeline.DryRunReport{}, false)
	got := buf.String()
	if !strings.Contains(got, "=== Dry Run: ws/repo → org/repo ===") {
		t.Errorf("expected dry run header, got:\n%s", got)
	}
}

func TestPrintDryRunReport_countsSummary(t *testing.T) {
	report := pipeline.DryRunReport{
		Entries: []pipeline.DryRunEntry{
			{Action: pipeline.ActionGitHubPR},
			{Action: pipeline.ActionGitHubPR},
			{Action: pipeline.ActionIssueImport},
			{Action: pipeline.ActionPlaceholder},
		},
	}
	var buf bytes.Buffer
	printDryRunReport(&buf, "ws/repo", "org/repo", report, false)
	got := buf.String()
	if !strings.Contains(got, "GitHub PR (branch exists):       2") {
		t.Errorf("expected GitHub PR count 2, got:\n%s", got)
	}
	if !strings.Contains(got, "Issue Import (merged/fallback):  1") {
		t.Errorf("expected Issue Import count 1, got:\n%s", got)
	}
	if !strings.Contains(got, "Placeholder (gap fill):          1") {
		t.Errorf("expected Placeholder count 1, got:\n%s", got)
	}
	if !strings.Contains(got, "Total:                           4") {
		t.Errorf("expected Total 4, got:\n%s", got)
	}
}

func TestPrintDryRunReport_verboseShowsEntries(t *testing.T) {
	report := pipeline.DryRunReport{
		Entries: []pipeline.DryRunEntry{
			{
				Action:   pipeline.ActionGitHubPR,
				PRNumber: 42,
				Title:    "My PR",
				Head:     "feature",
				Base:     "main",
				Body:     "body text",
			},
		},
	}
	var buf bytes.Buffer
	printDryRunReport(&buf, "ws/repo", "org/repo", report, true)
	got := buf.String()
	if !strings.Contains(got, "#42 My PR [GitHub PR: feature → main]") {
		t.Errorf("expected verbose entry, got:\n%s", got)
	}
	if !strings.Contains(got, "body text") {
		t.Errorf("expected body text in verbose output, got:\n%s", got)
	}
}
