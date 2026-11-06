package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"taskwatch/internal/domain"
)

func printStatus(out io.Writer, snap *domain.Snapshot) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tSTATE\tMR\tAPPROVALS\tBEHIND\tPIPELINE\tRELEASE\tSUMMARY")

	for _, t := range snap.Tasks {
		if len(t.MRs) == 0 {
			fmt.Fprintf(w, "%s\t%s\t-\t-\t-\t-\t-\t%s\n", t.ID, t.State, t.Summary)
			continue
		}
		for _, mr := range t.MRs {
			fmt.Fprintf(w, "%s\t%s\t%s\n", t.ID, t.State, mrColumns(mr, t.Summary))
		}
	}
	for _, mr := range snap.Orphans {
		fmt.Fprintf(w, "-\t-\t%s\n", mrColumns(mr, mr.Title))
	}
	w.Flush()

	if len(snap.Reviews) > 0 {
		fmt.Fprintln(out, "\nЖдут твоего ревью:")
		for _, mr := range snap.Reviews {
			fmt.Fprintf(out, "  %s  %s\n", mr.Title, mr.WebURL)
		}
	}
}

func mrColumns(mr *domain.MR, summary string) string {
	pipeline := "-"
	if p := mr.Pipeline; p != nil {
		pipeline = pipelineIcon(p.Outcome()) + " " + string(p.Status)
	}
	release := "-"
	if len(mr.Releases) > 0 {
		branches := make([]string, 0, len(mr.Releases))
		for _, r := range mr.Releases {
			branches = append(branches, r.Branch)
		}
		release = strings.Join(branches, ",")
	}
	return fmt.Sprintf("!%d %s\t%d\t%d\t%s\t%s\t%s",
		mr.IID, mr.State, mr.Approvals(), mr.Behind, pipeline, release, summary)
}

func pipelineIcon(o domain.Outcome) string {
	switch o {
	case domain.OutcomeSuccess:
		return "🟢"
	case domain.OutcomeFailed:
		return "🔴"
	case domain.OutcomeRunning:
		return "🟡"
	default:
		return "⚪"
	}
}
