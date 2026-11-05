package settings

import (
	"os"
	"strings"
)

// Имена записей в хранилище секретов (в Связке ключей — аккаунты сервиса com.taskwatch).
const (
	AccountGitLab   = "gitlab"
	AccountYouTrack = "youtrack"
)

// Accounts — все записи, которые заводит taskwatch.
var Accounts = []string{AccountGitLab, AccountYouTrack}

// SecretStore — где лежат токены. Отсутствие записи — пустая строка без ошибки.
type SecretStore interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// WithEnv — токены из TASKWATCH_GITLAB_TOKEN / TASKWATCH_YOUTRACK_TOKEN главнее
// хранилища. Когда переменная задана, хранилище не читается вовсе: не тратим
// время и не провоцируем лишних вопросов Связки ключей. Удобно для тестов и CI.
func WithEnv(s SecretStore) SecretStore { return envSecrets{s} }

type envSecrets struct{ SecretStore }

func (e envSecrets) Get(account string) (string, error) {
	if v := os.Getenv(EnvVar(account)); v != "" {
		return v, nil
	}
	return e.SecretStore.Get(account)
}

// EnvVar — имя env-переменной с токеном аккаунта: gitlab → TASKWATCH_GITLAB_TOKEN.
func EnvVar(account string) string { return "TASKWATCH_" + strings.ToUpper(account) + "_TOKEN" }
