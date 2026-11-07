// Package collect собирает таски и связанные MR в один доменный снапшот. Откуда
// они берутся, решают источники (порты TaskSource и MRSource); здесь — фильтры,
// параллельная догрузка деталей и привязка MR к таскам.
package collect

import (
	"context"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"taskwatch/internal/domain"
	"taskwatch/internal/failure"
)

// TaskSource — откуда берутся таски (YouTrack).
type TaskSource interface {
	// Tasks — таски без комментариев.
	Tasks(ctx context.Context) ([]*domain.Task, error)
	Comments(ctx context.Context, taskID string) ([]domain.Comment, error)
}

// MRSource — откуда берутся MR (GitLab).
type MRSource interface {
	// Authored — твои MR, обновлённые после since, без деталей.
	Authored(ctx context.Context, since time.Time) ([]*domain.MR, error)
	// ReviewRequested — открытые MR, где ты ревьюер, без деталей.
	ReviewRequested(ctx context.Context) ([]*domain.MR, error)
	// Load — MR со всеми деталями.
	Load(ctx context.Context, mr *domain.MR) (*domain.MR, error)
}

type Options struct {
	// Lookback — за какой период смотреть свои MR (в т.ч. смёрженные).
	Lookback time.Duration
	// ExcludeDraft — не показывать и не уведомлять о draft-MR. Убирают
	// draft-флажок — MR появится как новый и придёт уведомление.
	ExcludeDraft bool
	// Concurrency — сколько запросов деталей идёт одновременно; 0 — 6.
	Concurrency int
}

// Collector собирает снапшот. Состояния между вызовами не держит.
type Collector struct {
	tasks TaskSource // nil — YouTrack не настроен
	mrs   MRSource   // nil — GitLab не настроен
	opts  Options
}

func New(tasks TaskSource, mrs MRSource, opts Options) *Collector {
	return &Collector{tasks: tasks, mrs: mrs, opts: opts}
}

func (c *Collector) Collect(ctx context.Context) (*domain.Snapshot, error) {
	var (
		tasks            []*domain.Task
		authored, review []*domain.MR
	)

	// Списки независимы — запрашиваем разом. Ошибку помечаем службой: наружу
	// уходит первая, и без пометки не сказать, GitLab отвалился или YouTrack.
	g, gctx := errgroup.WithContext(ctx)
	if c.tasks != nil {
		g.Go(func() (err error) { tasks, err = c.tasks.Tasks(gctx); return failure.Wrap(failure.YouTrack, err) })
	}
	if c.mrs != nil {
		since := time.Now().Add(-c.opts.Lookback)
		g.Go(func() (err error) {
			authored, err = c.mrs.Authored(gctx, since)
			return failure.Wrap(failure.GitLab, err)
		})
		g.Go(func() (err error) {
			review, err = c.mrs.ReviewRequested(gctx)
			return failure.Wrap(failure.GitLab, err)
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	authored = slices.DeleteFunc(authored, func(mr *domain.MR) bool { return mr.State == domain.MRClosed || c.skipDraft(mr) })
	review = slices.DeleteFunc(review, c.skipDraft)

	// Детали — по запросу на таску и по нескольку на MR; ограничиваем параллельность.
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(c.concurrency())
	for _, t := range tasks {
		g.Go(func() (err error) {
			t.Comments, err = c.tasks.Comments(gctx, t.ID)
			return failure.Wrap(failure.YouTrack, err)
		})
	}
	for _, list := range [][]*domain.MR{authored, review} {
		for i, mr := range list {
			g.Go(func() (err error) { list[i], err = c.mrs.Load(gctx, mr); return failure.Wrap(failure.GitLab, err) })
		}
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	snap := &domain.Snapshot{Tasks: tasks, Reviews: review}
	byID := make(map[string]*domain.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, mr := range authored {
		if t, ok := byID[domain.IssueKey(mr.SourceBranch, mr.Title)]; ok {
			t.MRs = append(t.MRs, mr)
		} else {
			snap.Orphans = append(snap.Orphans, mr)
		}
	}
	return snap, nil
}

func (c *Collector) skipDraft(mr *domain.MR) bool { return c.opts.ExcludeDraft && mr.Draft }

func (c *Collector) concurrency() int {
	if c.opts.Concurrency > 0 {
		return c.opts.Concurrency
	}
	return 6
}
