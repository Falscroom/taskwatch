// Package app собирает зависимости движка из настроек — единственное место,
// где конфиг превращается в клиенты GitLab и YouTrack.
package app

import (
	"taskwatch/internal/alerts"
	"taskwatch/internal/collect"
	"taskwatch/internal/config"
	"taskwatch/internal/engine"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/youtrack"
)

// Deps — engine.Factory: источники только для настроенных сервисов.
func Deps(cfg *config.Config) engine.Deps {
	var (
		tasks      collect.TaskSource
		mrs        collect.MRSource
		transition alerts.Transitioner
	)
	if cfg.YouTrackOK() {
		yt := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token)
		tasks = youtrack.NewSource(yt, cfg.YouTrack.Query)
		transition = yt.Command
	}
	if cfg.GitLabOK() {
		gl := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
		mrs = gitlab.NewSource(gl, cfg.GitLab.Username, cfg.Rules.ReleaseBranchPrefix)
	}
	return engine.Deps{
		Collector: collect.New(tasks, mrs, collect.Options{
			Lookback:     cfg.Rules.MRLookback,
			ExcludeDraft: cfg.Rules.ExcludeDraft,
		}),
		Transition: transition,
	}
}
