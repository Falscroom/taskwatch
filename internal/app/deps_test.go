package app

import (
	"context"
	"testing"

	"taskwatch/internal/config"
	"taskwatch/internal/testutil/fakeapi"
)

func TestDepsOnlyForConfiguredServices(t *testing.T) {
	srv := fakeapi.New(t)
	cfg := config.Default("/x/config.yaml")

	d := Deps(cfg)
	if d.Transition != nil {
		t.Error("transition without YouTrack")
	}
	snap, err := d.Collector.Collect(context.Background())
	if err != nil || len(snap.AllMRs()) != 0 || len(srv.Requests()) != 0 {
		t.Errorf("unconfigured collector: %+v, %v", snap, err)
	}

	cfg.GitLab = config.GitLab{URL: srv.URL, Token: "t", Username: "me"}
	if _, err := Deps(cfg).Collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		if r[:len("GET /api/v4/")] != "GET /api/v4/" {
			t.Errorf("YouTrack queried while not configured: %s", r)
		}
	}

	cfg.YouTrack = config.YouTrack{URL: srv.URL, Token: "t", Login: "me", Query: "q"}
	d = Deps(cfg)
	if d.Transition == nil {
		t.Fatal("no transition with YouTrack configured")
	}
	if err := d.Transition(context.Background(), "ABC-1", "State QA"); err != nil {
		t.Fatal(err)
	}
	if cmds := srv.Commands(); len(cmds) != 1 || cmds[0].IssueID != "ABC-1" {
		t.Errorf("commands = %+v", cmds)
	}
}
