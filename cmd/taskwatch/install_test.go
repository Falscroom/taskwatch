package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveOldInstall(t *testing.T) {
	home := t.TempDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.taskwatch.plist")
	old := filepath.Join(home, ".local", "share", "taskwatch")
	link := filepath.Join(home, ".local", "bin", "taskwatch")
	other := filepath.Join(home, ".local", "bin", "other")
	for _, d := range []string{filepath.Dir(plist), old, filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{plist, filepath.Join(old, "README.md"), other} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(old, "taskwatch.app", "Contents", "MacOS", "taskwatch"), link); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XPC_SERVICE_NAME", "")
	calls := 0
	if !removeOldInstall(home, func() error { calls++; return nil }) {
		t.Fatal("old install must be found")
	}
	if calls != 1 {
		t.Errorf("bootout calls = %d", calls)
	}
	for _, p := range []string{plist, old, link} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("unrelated files must stay")
	}

	// Второй запуск: прежней установки уже нет.
	if removeOldInstall(home, func() error { calls++; return nil }) || calls != 1 {
		t.Error("nothing to migrate the second time")
	}
}

// Запущенный самим LaunchAgent'ом не выключает себя.
func TestRemoveOldInstallUnderLaunchAgent(t *testing.T) {
	home := t.TempDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.taskwatch.plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XPC_SERVICE_NAME", "com.taskwatch")
	called := false
	if !removeOldInstall(home, func() error { called = true; return nil }) || called {
		t.Errorf("bootout called = %v", called)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Error("plist must be removed")
	}
}

func TestLogToFile(t *testing.T) {
	defer log.SetOutput(os.Stderr)
	path := filepath.Join(t.TempDir(), "sub", "taskwatch.log")
	logToFile(path)
	log.Print("hello")
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "hello") {
		t.Fatalf("log file = %q, %v", data, err)
	}

	// Разросшийся лог уходит в .old, новый начинается с нуля.
	if err := os.WriteFile(path, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	logToFile(path)
	if fi, err := os.Stat(path + ".old"); err != nil || fi.Size() != 2<<20 {
		t.Errorf("old log: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() != 0 {
		t.Errorf("new log size = %v, %v", fi.Size(), err)
	}
}
