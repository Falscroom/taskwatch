// Package settings — сценарии вокруг настроек: загрузить конфиг вместе с токенами,
// сохранить форму, проверить связь с сервисами, подобрать запрос тасок. Токены
// живут в хранилище секретов (Связка ключей), в yaml — только настройки.
package settings

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"

	"taskwatch/internal/config"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/youtrack"
)

type Service struct {
	path    string
	secrets SecretStore
}

// New — настройки из файла path с токенами из secrets.
func New(path string, secrets SecretStore) *Service {
	return &Service{path: path, secrets: secrets}
}

// Path — где лежит config.yaml.
func (s *Service) Path() string { return s.path }

// tokenField — токен сервиса в конфиге и его запись в хранилище.
type tokenField struct {
	account string
	token   *string
}

func tokenFields(cfg *config.Config) []tokenField {
	return []tokenField{
		{AccountGitLab, &cfg.GitLab.Token},
		{AccountYouTrack, &cfg.YouTrack.Token},
	}
}

// Load читает конфиг и подставляет токены из хранилища. Файл не прочитался
// или не разобрался — (nil, err). Конфиг, не прошедший проверку (например,
// токен недоступен этой сборке), возвращается вместе с ошибкой: окно настроек
// покажет его заполненным, а не пустым.
func (s *Service) Load() (*config.Config, error) {
	cfg, err := config.Load(s.path)
	if err != nil {
		return nil, err
	}
	s.migrateLegacyTokens(cfg)
	for _, f := range tokenFields(cfg) {
		if *f.token != "" {
			continue // токен из yaml, который не удалось перенести
		}
		v, err := s.secrets.Get(f.account)
		if err != nil {
			log.Printf("config: не смог прочитать токен %s: %v", f.account, err)
			continue
		}
		*f.token = v
	}
	return cfg, cfg.Validate()
}

// migrateLegacyTokens переносит открытые токены из yaml старых версий в
// хранилище и перезаписывает файл без них. Если хоть один перенести не вышло,
// файл не трогаем — лучше открытый токен, чем отвалившийся монитор; при
// следующем запуске попробуем снова.
func (s *Service) migrateLegacyTokens(cfg *config.Config) {
	migrated, failed := false, false
	for _, f := range tokenFields(cfg) {
		if *f.token == "" {
			continue
		}
		if err := s.secrets.Set(f.account, *f.token); err != nil {
			log.Printf("config: не смог спрятать токен %s в связку ключей (%v) — оставил в yaml", f.account, err)
			failed = true
			continue
		}
		migrated = true
		log.Printf("config: токен %s перенесён из config.yaml в связку ключей", f.account)
	}
	if !migrated || failed {
		return
	}
	if err := config.Save(s.path, cfg); err != nil {
		// Не критично: при следующем запуске миграция повторится тем же значением.
		log.Printf("config: токены спрятаны, но yaml не перезаписан: %v", err)
	}
}

// Resolve готовит конфиг из формы к сохранению: переносит прежние токены
// (CarryTokens) и проверяет, что каждый сервис пуст или заполнен целиком.
func Resolve(cur, next *config.Config) error {
	if err := CarryTokens(cur, next); err != nil {
		return err
	}
	return next.Validate()
}

// CarryTokens подставляет в конфиг из формы прежние токены. Форма не знает
// токенов (GET их не отдаёт), поэтому пустой токен значит «оставить прежний».
// Прежний токен переносится только на тот же адрес сервиса: при смене URL его
// надо ввести заново, иначе он уйдёт на новый хост. Очищенный URL — сервис
// выключают, токен не переносится.
func CarryTokens(cur, next *config.Config) error {
	var errs []error
	carry := func(name, curURL, nextURL, curToken string, nextToken *string) {
		switch {
		case *nextToken != "" || curToken == "" || strings.TrimSpace(nextURL) == "":
		case sameEndpoint(curURL, nextURL):
			*nextToken = curToken
		default:
			errs = append(errs, fmt.Errorf("%s: адрес изменился — введи токен заново", name))
		}
	}
	carry("gitlab", cur.GitLab.URL, next.GitLab.URL, cur.GitLab.Token, &next.GitLab.Token)
	carry("youtrack", cur.YouTrack.URL, next.YouTrack.URL, cur.YouTrack.Token, &next.YouTrack.Token)
	return errors.Join(errs...)
}

// sameEndpoint — адреса указывают на одно и то же: регистр хоста и
// завершающий слэш не важны.
func sameEndpoint(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimRight(strings.TrimSpace(s), "/")
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return s
		}
		return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.Path
	}
	return norm(a) == norm(b)
}

// Save сохраняет конфиг из формы: изменившиеся токены — в хранилище, остальное —
// в yaml. next должен пройти Resolve.
func (s *Service) Save(cur, next *config.Config) error {
	curTokens := tokenFields(cur)
	for i, f := range tokenFields(next) {
		if *f.token != "" && *f.token != *curTokens[i].token {
			if err := s.secrets.Set(f.account, *f.token); err != nil {
				return fmt.Errorf("store token: %w", err)
			}
		}
	}
	return config.Save(s.path, next)
}

// ClearSecrets удаляет все токены из хранилища: кнопка «Удалить токены из Связки ключей» и команда secret-clear.
func (s *Service) ClearSecrets() error {
	var errs []error
	for _, acc := range Accounts {
		if err := s.secrets.Delete(acc); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", acc, err))
		}
	}
	return errors.Join(errs...)
}

// Probe — результат проверки связи с одним сервисом.
type Probe struct {
	Skipped bool   // сервис не заполнен — проверять нечего
	User    string // владелец токена
	Err     error
}

// Test проверяет связь с сервисами конфига: токен рабочий и чей он.
func Test(ctx context.Context, cfg *config.Config) (gl, yt Probe) {
	gl, yt = Probe{Skipped: true}, Probe{Skipped: true}
	if cfg.GitLabOK() {
		u, err := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token).CurrentUser(ctx)
		gl = Probe{User: u.Username, Err: err}
	}
	if cfg.YouTrackOK() {
		login, err := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token).CurrentUser(ctx)
		yt = Probe{User: login, Err: err}
	}
	return gl, yt
}

// ErrNoYouTrack — для списка досок не хватает адреса или токена YouTrack.
var ErrNoYouTrack = errors.New("заполни URL и токен YouTrack")

// Board — доска YouTrack с идущим спринтом и запрос тасок этого спринта.
type Board struct {
	Name   string
	Sprint string
	Query  string
}

// Boards — доски YouTrack с текущим спринтом, по алфавиту: из них форма
// подставляет запрос тасок. Нужны только адрес и токен — логин и запрос
// как раз ещё не заполнены.
func Boards(ctx context.Context, cfg *config.Config) ([]Board, error) {
	if cfg.YouTrack.URL == "" || cfg.YouTrack.Token == "" {
		return nil, ErrNoYouTrack
	}
	agiles, err := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token).Agiles(ctx)
	if err != nil {
		return nil, err
	}
	var out []Board
	for _, a := range agiles {
		if a.CurrentSprint != nil {
			out = append(out, Board{Name: a.Name, Sprint: a.CurrentSprint.Name, Query: youtrack.SprintQuery(a.Name)})
		}
	}
	slices.SortFunc(out, func(a, b Board) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}
