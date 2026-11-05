package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMissingIsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Fresh() {
		t.Fatal("missing file must be fresh")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if s.Fresh() {
		t.Error("fresh after save")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file left behind: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).Truncate(time.Second)
	s.Mark("ev:1")
	s.Mute("mr:1", until)
	s.Mute("title:Bump", time.Time{})
	s.SetCounts(Counts{Own: 3, Reviews: 2, Tasks: 1})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fresh() {
		t.Error("reopened store is fresh")
	}
	if !r.Seen("ev:1") || r.Seen("ev:2") {
		t.Error("seen not persisted")
	}
	m := r.Mutes()
	if !m["mr:1"].Equal(until) || !m["title:Bump"].IsZero() || len(m) != 2 {
		t.Errorf("mutes = %v", m)
	}
	if r.Counts() != (Counts{Own: 3, Reviews: 2, Tasks: 1}) {
		t.Errorf("counts = %+v", r.Counts())
	}

	r.Unmute("mr:1")
	if _, ok := r.Mutes()["mr:1"]; ok {
		t.Error("unmute")
	}
}

func TestMutesReturnsCopy(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	s.Mute("mr:1", time.Time{})
	s.Mutes()["mr:2"] = time.Time{}
	if _, ok := s.Mutes()["mr:2"]; ok {
		t.Error("Mutes leaks internal map")
	}
}

func TestSavePrunes(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	s.seen["old"] = time.Now().Add(-retention - time.Hour)
	s.seen["recent"] = time.Now().Add(-retention + time.Hour)
	s.Mute("expired", time.Now().Add(-time.Second))
	s.Mute("active", time.Now().Add(time.Hour))
	s.Mute("forever", time.Time{})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if s.Seen("old") || !s.Seen("recent") {
		t.Error("seen retention")
	}
	m := s.Mutes()
	if _, ok := m["expired"]; ok || len(m) != 2 {
		t.Errorf("mutes after save = %v", m)
	}
}

func TestOpenCorrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("corrupted state must fail to open")
	}
}

func TestOpenNullMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"seen":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Mark("x") // не паникует на nil-карте
	s.Mute("y", time.Time{})
}
