package main

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"taskwatch/internal/config"
	"taskwatch/internal/settings"
	"taskwatch/internal/testutil/fakesecrets"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func serviceWith(t *testing.T, yaml string, store settings.SecretStore) *settings.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if yaml != "" {
		if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return settings.New(path, store)
}

// Регрессия: токен в Связке ключей недоступен новой сборке → конфиг неполон.
// Раньше UI-режим подменял его пустым дефолтом и форма теряла все настройки.
func TestLoadConfigKeepsIncompleteConfigForUI(t *testing.T) {
	store := fakesecrets.New(nil)
	store.GetErr[settings.AccountGitLab] = errors.New("access denied")
	svc := serviceWith(t, "gitlab:\n  url: https://gl.example\n  username: me\nrules:\n  review_state: Code Review\n", store)

	cfg, err := loadConfig(svc, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitLab.URL != "https://gl.example" || cfg.GitLab.Username != "me" || cfg.Rules.ReviewState != "Code Review" {
		t.Errorf("cfg = %+v, settings must survive a missing token", cfg)
	}

	if _, err := loadConfig(svc, false); err == nil {
		t.Error("non-UI commands need a valid config")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	svc := serviceWith(t, "", fakesecrets.New(nil))
	cfg, err := loadConfig(svc, true)
	if err != nil || cfg == nil || cfg.PollInterval != config.DefaultPollInterval || cfg.Configured() {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
	if _, err := loadConfig(svc, false); err == nil {
		t.Error("missing config is fatal outside UI modes")
	}
}

func TestLoadConfigValid(t *testing.T) {
	svc := serviceWith(t, "gitlab:\n  url: https://gl\n  username: me\n",
		fakesecrets.New(map[string]string{settings.AccountGitLab: "tok"}))
	cfg, err := loadConfig(svc, false)
	if err != nil || !cfg.GitLabOK() {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
}
