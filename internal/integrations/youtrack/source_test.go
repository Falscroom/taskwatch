package youtrack_test

import (
	"context"
	"testing"

	"taskwatch/internal/domain"
	"taskwatch/internal/integrations/httpx"
	"taskwatch/internal/integrations/youtrack"
	"taskwatch/internal/testutil/fakeapi"
)

func TestSource(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetIssues(fakeapi.Issue{
		Issue: youtrack.Issue{ID: "ABC-1", Summary: "Login", CustomFields: []youtrack.CustomField{fakeapi.State("Open")}},
		Comments: []youtrack.Comment{{ID: "c1", Text: "hi", Author: struct {
			Login string `json:"login"`
		}{"bob"}}},
	})
	src := youtrack.NewSource(youtrack.New(srv.URL, "tok", httpx.WithRetryDelays()), "for: me")

	tasks, err := src.Tasks(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %+v, %v", tasks, err)
	}
	if tk := tasks[0]; tk.ID != "ABC-1" || tk.Summary != "Login" || tk.State != "Open" || tk.URL != srv.URL+"/issue/ABC-1" ||
		tk.Comments != nil || tk.MRs != nil {
		t.Errorf("task = %+v", tk)
	}
	comments, err := src.Comments(context.Background(), "ABC-1")
	if err != nil || len(comments) != 1 || comments[0] != (domain.Comment{ID: "c1", Author: "bob", Text: "hi"}) {
		t.Errorf("comments = %+v, %v", comments, err)
	}

	srv.FailWith(500)
	if _, err := src.Tasks(context.Background()); err == nil {
		t.Error("tasks error")
	}
	if _, err := src.Comments(context.Background(), "ABC-1"); err == nil {
		t.Error("comments error")
	}
}
