// Команда taskwatch-demo — окно taskwatch на поддельных GitLab и YouTrack с
// примерами карточек: для работы над фронтендом без настоящих аккаунтов.
// В браузере (make demo) или в меню-баре, как настоящее приложение (make demo-app).
// Ничего не трогает: ни Связку ключей, ни ~/.config, уведомления пишутся в лог.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"taskwatch/internal/alerts"
	"taskwatch/internal/app"
	"taskwatch/internal/config"
	"taskwatch/internal/engine"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/youtrack"
	"taskwatch/internal/settings"
	"taskwatch/internal/state"
	"taskwatch/internal/testutil/fakeapi"
	"taskwatch/internal/testutil/fakesecrets"
	"taskwatch/internal/ui/frontend"
	"taskwatch/internal/ui/httpapi"
	"taskwatch/internal/ui/tray"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7778", "listen address (браузерный режим)")
	inTray := flag.Bool("app", false, "окно в меню-баре вместо браузера")
	flag.Parse()
	if err := run(*addr, *inTray); err != nil {
		log.Fatal(err)
	}
}

func run(addr string, inTray bool) error {
	srv := fakeapi.Start()
	defer srv.Close()
	seed(srv)

	// Каталог постоянный и обнуляется при запуске: из меню-бара процесс
	// завершает Wails, и defer до очистки не доходит.
	mode := "web"
	if inTray {
		mode = "app"
	}
	dir := filepath.Join(os.TempDir(), "taskwatch-demo-"+mode)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "config.yaml")
	cfg := config.Default(path)
	cfg.GitLab = config.GitLab{URL: srv.URL, Token: "demo", Username: "me"}
	cfg.YouTrack = config.YouTrack{URL: srv.URL, Token: "demo", Login: "me", Query: "for: me"}
	cfg.Rules.ReviewState = "Code Review"
	cfg.Rules.NextCommand = "State Ready for QA"
	cfg.Rules.MRBehindCommits = 10
	cfg.PollInterval = 30 * time.Second

	store, err := state.Open(cfg.StatePath)
	if err != nil {
		return err
	}
	// Демо не в бандле: Центр уведомлений недоступен, уведомления — в лог.
	notifier := tray.NewNotifier(func(ev alerts.Event) error {
		log.Printf("уведомление: %s · %s — %s", ev.Title, ev.Subtitle, ev.Body)
		return nil
	})
	eng := engine.New(cfg, store, notifier.Notify, app.Deps)
	svc := settings.New(path, fakesecrets.New(map[string]string{settings.AccountGitLab: "demo", settings.AccountYouTrack: "demo"}))

	if inTray {
		// Цикл опроса tray запускает сам; выход — «Выйти» в меню иконки.
		log.Printf("демо в меню-баре (поддельные GitLab и YouTrack на %s)", srv.URL)
		return tray.Run(eng, svc, notifier)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go eng.Run(ctx)

	openURL := func(url string) error { return exec.Command("open", url).Run() }
	log.Printf("демо: http://%s (поддельные GitLab и YouTrack на %s)", addr, srv.URL)
	return httpapi.Serve(ctx, addr, httpapi.Handler(eng, svc, openURL, http.FileServerFS(frontend.FS())))
}

// seed — по карточке на каждую группу окна.
func seed(srv *fakeapi.Server) {
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }
	me := gitlab.User{Username: "me", Name: "Иван Петров"}
	anna := gitlab.User{Username: "anna", Name: "Анна Смирнова"}
	oleg := gitlab.User{Username: "oleg", Name: "Олег Котов"}
	mr := func(id int, title, branch string, opts func(*gitlab.MergeRequest)) gitlab.MergeRequest {
		m := gitlab.MergeRequest{
			ID: 1000 + id, IID: id, ProjectID: 1, Title: title, SourceBranch: branch, State: "opened",
			WebURL: "https://gitlab.example.com/app/-/merge_requests/" + string(rune('0'+id)), SHA: "sha" + branch,
			Author: me, CreatedAt: ago(6 * time.Hour), BlockingDiscussionsResolved: true, UserNotesCount: 2,
			HeadPipeline: &gitlab.Pipeline{ID: id, Status: "success", WebURL: "https://gitlab.example.com/app/-/pipelines/1"},
		}
		if opts != nil {
			opts(&m)
		}
		return m
	}
	state := func(s string) []youtrack.CustomField { return []youtrack.CustomField{fakeapi.State(s)} }

	srv.SetIssues(
		fakeapi.Issue{Issue: youtrack.Issue{ID: "APP-101", Summary: "Вход по SSO", CustomFields: state("In Progress")}},
		fakeapi.Issue{Issue: youtrack.Issue{ID: "APP-102", Summary: "Экспорт отчёта в CSV", CustomFields: state("Code Review")}},
		fakeapi.Issue{Issue: youtrack.Issue{ID: "APP-103", Summary: "Кэш списка проектов", CustomFields: state("Code Review")}},
		fakeapi.Issue{Issue: youtrack.Issue{ID: "APP-104", Summary: "Поправить вёрстку профиля", CustomFields: state("Open")}},
		fakeapi.Issue{Issue: youtrack.Issue{ID: "APP-99", Summary: "Лимиты API", CustomFields: state("Done")}},
	)
	srv.SetAgiles(
		youtrack.Agile{Name: "Команда А", CurrentSprint: &youtrack.Sprint{Name: "Спринт 42"}},
		youtrack.Agile{Name: "Мобилка", CurrentSprint: &youtrack.Sprint{Name: "Sprint 7"}},
		youtrack.Agile{Name: "Поддержка"},
	)
	srv.SetMine(
		fakeapi.MR{MergeRequest: mr(1, "Вход по SSO", "feature/app-101-sso", func(m *gitlab.MergeRequest) {
			m.HeadPipeline.Status = "failed"
			m.HasConflicts = true
			m.DivergedCommitsCount = 14
		}), Reviewers: []gitlab.Reviewer{{User: anna, State: "requested_changes"}}},
		fakeapi.MR{MergeRequest: mr(2, "Экспорт отчёта в CSV", "feature/app-102-csv", nil),
			ApprovedBy: []string{"anna", "oleg"},
			Reviewers:  []gitlab.Reviewer{{User: anna, State: "approved"}, {User: oleg, State: "approved"}}},
		fakeapi.MR{MergeRequest: mr(3, "Кэш списка проектов", "feature/app-103-cache", func(m *gitlab.MergeRequest) {
			m.CreatedAt = ago(53 * time.Hour)
			m.HeadPipeline.Status = "running"
		}), ApprovedBy: []string{"oleg"}, Reviewers: []gitlab.Reviewer{{User: anna, State: "unreviewed"}, {User: oleg, State: "approved"}}},
		fakeapi.MR{MergeRequest: mr(4, "Лимиты API", "feature/app-99-limits", func(m *gitlab.MergeRequest) {
			m.State = "merged"
			m.MergeCommitSHA = "merged-4"
		}), Branches: []string{"main", "release/2026.11"}},
		fakeapi.MR{MergeRequest: mr(5, "Обновить зависимости", "chore/deps", func(m *gitlab.MergeRequest) {
			m.CreatedAt = ago(3 * time.Hour)
		})},
	)
	srv.SetReviews(
		fakeapi.MR{MergeRequest: mr(6, "Новый онбординг", "feature/onboarding", func(m *gitlab.MergeRequest) {
			m.Author = anna
			m.CreatedAt = ago(28 * time.Hour)
		}), Reviewers: []gitlab.Reviewer{{User: me, State: "unreviewed"}}},
		fakeapi.MR{MergeRequest: mr(7, "Метрики латентности", "feature/latency", func(m *gitlab.MergeRequest) {
			m.Author = oleg
		}), ApprovedBy: []string{"me"}, Reviewers: []gitlab.Reviewer{{User: me, State: "approved"}}},
	)
}
