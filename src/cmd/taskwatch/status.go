package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"taskwatch/internal/watch"
)

func printStatus(out io.Writer, snap *watch.Snapshot) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tSTATE\tMR\tAPPROVALS\tBEHIND\tPIPELINE\tRELEASE\tSUMMARY")

	for _, t := range snap.Tasks {
		if len(t.MRs) == 0 {
			fmt.Fprintf(w, "%s\t%s\t-\t-\t-\t-\t-\t%s\n", t.Issue.ID, t.State, t.Issue.Summary)
			continue
		}
		for _, mr := range t.MRs {
			fmt.Fprintf(w, "%s\t%s\t%s\n", t.Issue.ID, t.State, mrColumns(mr, t.Issue.Summary))
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

func mrColumns(mr *watch.MR, summary string) string {
	pipeline := "-"
	if p := mr.HeadPipeline; p != nil {
		pipeline = pipelineIcon(p.Status) + " " + p.Status
	}
	release := "-"
	if len(mr.ReleaseBranches) > 0 {
		release = strings.Join(mr.ReleaseBranches, ",")
	}
	return fmt.Sprintf("!%d %s\t%d\t%d\t%s\t%s\t%s",
		mr.IID, mr.State, mr.Approvals, mr.DivergedCommitsCount, pipeline, release, summary)
}

func pipelineIcon(status string) string {
	switch status {
	case "success":
		return "🟢"
	case "failed", "canceled":
		return "🔴"
	case "running", "pending", "created", "waiting_for_resource", "preparing", "manual", "scheduled":
		return "🟡"
	default:
		return "⚪"
	}
}
