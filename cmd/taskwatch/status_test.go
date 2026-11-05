package main

import (
	"bytes"
	"strings"
	"testing"

	"taskwatch/internal/domain"
)

func TestPrintStatus(t *testing.T) {
	mr := &domain.MR{IID: 7, State: domain.MROpened, Title: "Fix", Behind: 3,
		Pipeline: &domain.Pipeline{Status: "failed"}, ApprovedBy: []string{"a", "b"}}
	merged := &domain.MR{IID: 8, State: domain.MRMerged, Title: "Old", ReleaseBranches: []string{"release/1", "release/2"}}
	snap := &domain.Snapshot{
		Tasks: []*domain.Task{
			{ID: "ABC-1", Summary: "Login", State: "Code Review", MRs: []*domain.MR{mr}},
			{ID: "ABC-2", Summary: "Idle", State: "Open"},
		},
		Orphans: []*domain.MR{merged},
		Reviews: []*domain.MR{{Title: "Their MR", WebURL: "https://gl/9"}},
	}
	var buf bytes.Buffer
	printStatus(&buf, snap)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	fields := func(i int) string { return strings.Join(strings.Fields(lines[i]), " ") }

	want := []string{
		"TASK STATE MR APPROVALS BEHIND PIPELINE RELEASE SUMMARY",
		"ABC-1 Code Review !7 opened 2 3 🔴 failed - Login",
		"ABC-2 Open - - - - - Idle",
		"- - !8 merged 0 0 - release/1,release/2 Old",
		"",
		"Ждут твоего ревью:",
		"Their MR https://gl/9",
	}
	if len(lines) != len(want) {
		t.Fatalf("output:\n%s", buf.String())
	}
	for i, w := range want {
		if got := fields(i); got != w {
			t.Errorf("line %d = %q, want %q", i, got, w)
		}
	}
}

func TestPipelineIcon(t *testing.T) {
	cases := map[domain.PipelineStatus]string{
		"success": "🟢", "failed": "🔴", "canceled": "🔴", "running": "🟡", "manual": "🟡", "skipped": "⚪", "": "⚪",
	}
	for status, want := range cases {
		if got := pipelineIcon(status.Outcome()); got != want {
			t.Errorf("pipelineIcon(%q) = %q, want %q", status, got, want)
		}
	}
}
