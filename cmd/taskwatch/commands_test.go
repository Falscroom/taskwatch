package main

import (
	"strings"
	"testing"

	"taskwatch/internal/settings"
	"taskwatch/internal/testutil/fakeapi"
	"taskwatch/internal/testutil/fakesecrets"
)

func TestCommandTable(t *testing.T) {
	u := usage()
	seen := map[string]bool{}
	for _, c := range commands {
		if seen[c.name] {
			t.Errorf("duplicate command %q", c.name)
		}
		seen[c.name] = true
		if c.run == nil || c.summary == "" {
			t.Errorf("command %q is incomplete", c.name)
		}
		if !strings.Contains(u, "  "+c.name+" ") {
			t.Errorf("usage lacks %q:\n%s", c.name, u)
		}
		if got, ok := lookup(c.name); !ok || got.name != c.name {
			t.Errorf("lookup(%q) = %v, %v", c.name, got.name, ok)
		}
	}
	if _, ok := lookup("nope"); ok {
		t.Error("unknown command found")
	}
	for name, need := range map[string]configNeed{"app": relaxedConfig, "web": relaxedConfig, "once": validConfig, "secret-clear": noConfig} {
		if c, _ := lookup(name); c.config != need {
			t.Errorf("%s needs config %v, want %v", name, c.config, need)
		}
	}
}

func TestSecretClearNeedsNoConfig(t *testing.T) {
	store := fakesecrets.New(map[string]string{settings.AccountGitLab: "a", settings.AccountYouTrack: "b"})
	cmd, _ := lookup("secret-clear")
	// Конфига нет вовсе — команда всё равно работает.
	err := execute(cmd, setup{settings: settings.New(t.TempDir()+"/missing.yaml", store)})
	if err != nil || len(store.All()) != 0 {
		t.Errorf("err = %v, store = %v", err, store.All())
	}
}

func TestOnceRequiresValidConfig(t *testing.T) {
	cmd, _ := lookup("once")
	svc := serviceWith(t, "", fakesecrets.New(nil))
	if err := execute(cmd, setup{settings: svc}); err == nil {
		t.Error("once without config must fail")
	}
}

func TestOnceTicks(t *testing.T) {
	srv := fakeapi.New(t)
	svc := serviceWith(t, "gitlab:\n  url: "+srv.URL+"\n  username: me\n",
		fakesecrets.New(map[string]string{settings.AccountGitLab: "tok"}))
	var sent int
	notify := func(title, message, url, group string) error { sent++; return nil }
	cmd, _ := lookup("once")
	if err := execute(cmd, setup{settings: svc, notify: notify}); err != nil {
		t.Fatal(err)
	}
	if len(srv.Requests()) == 0 || sent != 0 {
		t.Errorf("requests = %v, sent = %d (first run must not notify)", srv.Requests(), sent)
	}
}
