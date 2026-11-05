// Package fakesecrets — хранилище секретов в памяти для тестов: настоящую
// Связку ключей тесты не трогают.
package fakesecrets

import (
	"maps"
	"sync"
)

type Store struct {
	mu      sync.Mutex
	secrets map[string]string
	// GetErr / SetErr — ошибки, которые вернут Get / Set для аккаунта.
	GetErr map[string]error
	SetErr map[string]error
}

// New — хранилище с заданными секретами (аккаунт → секрет).
func New(secrets map[string]string) *Store {
	s := &Store{secrets: map[string]string{}, GetErr: map[string]error{}, SetErr: map[string]error{}}
	maps.Copy(s.secrets, secrets)
	return s
}

func (s *Store) Get(account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.GetErr[account]; err != nil {
		return "", err
	}
	return s.secrets[account], nil
}

func (s *Store) Set(account, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.SetErr[account]; err != nil {
		return err
	}
	s.secrets[account] = secret
	return nil
}

func (s *Store) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, account)
	return nil
}

// All — копия содержимого.
func (s *Store) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.secrets)
}
