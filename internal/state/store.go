// Package state хранит ключи уже отправленных уведомлений, чтобы каждое событие приходило
// один раз, и заглушения MR — их карточки скрыты, а уведомления подавлены.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const retention = 90 * 24 * time.Hour

type file struct {
	Seen  map[string]time.Time `json:"seen"`
	Mutes map[string]time.Time `json:"mutes,omitempty"`
	// Counts — размеры последнего принятого снапшота; переживают рестарт, чтобы
	// первый после запуска тик мог отличить «GitLab вернул пустоту» от «и правда пусто».
	Counts Counts `json:"counts"`
}

// Counts — сколько карточек было в последнем хорошем снапшоте, по разделам.
type Counts struct {
	Own     int `json:"own"`     // свои MR: привязанные к таскам + сироты
	Reviews int `json:"reviews"` // MR, где ты ревьюер
	Tasks   int `json:"tasks"`   // таски YouTrack
}

type Store struct {
	mu     sync.Mutex
	path   string
	seen   map[string]time.Time
	mutes  map[string]time.Time // ключ карточки → до когда заглушен; zero = навсегда
	counts Counts               // см. file.Counts
	fresh  bool
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, seen: map[string]time.Time{}, mutes: map[string]time.Time{}}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.fresh = true
		return s, nil
	}
	if err != nil {
		return nil, err
	}

	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Seen != nil {
		s.seen = f.Seen
	}
	if f.Mutes != nil {
		s.mutes = f.Mutes
	}
	s.counts = f.Counts
	return s, nil
}

// Fresh — первый запуск: события надо запомнить, но не слать, иначе прилетит вся история.
func (s *Store) Fresh() bool { return s.fresh }

func (s *Store) Seen(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[key]
	return ok
}

func (s *Store) Mark(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[key] = time.Now()
}

// Mute глушит ключ до until; zero значит навсегда.
func (s *Store) Mute(key string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutes[key] = until
}

func (s *Store) Unmute(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mutes, key)
}

// Mutes — копия заглушений. Активные: until zero или в будущем, проверка через Active.
func (s *Store) Mutes() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Time, len(s.mutes))
	for k, t := range s.mutes {
		out[k] = t
	}
	return out
}

// Active — действует ли заглушение until на момент now.
func Active(until, now time.Time) bool { return until.IsZero() || until.After(now) }

// Counts — размеры последнего принятого снапшота (см. file.Counts).
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts
}

// SetCounts запоминает размеры снапшота; попадают в файл при ближайшем Save.
func (s *Store) SetCounts(c Counts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts = c
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}
	for k, until := range s.mutes {
		if !Active(until, time.Now()) {
			delete(s.mutes, k)
		}
	}

	data, err := json.MarshalIndent(file{Seen: s.seen, Mutes: s.mutes, Counts: s.counts}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.fresh = false
	return nil
}
