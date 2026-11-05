package keychain

import "testing"

// Roundtrip на временной записи: до реальных токенов не касается.
func TestSaveLoadDelete(t *testing.T) {
	const account = "test-roundtrip"
	defer Delete(account) // уборка даже при падении

	if err := Save(account, "first"); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Второй Save идёт по ветке «запись уже есть» — пересоздание.
	if err := Save(account, "second"); err != nil {
		t.Fatalf("resave: %v", err)
	}
	got, err := Load(account)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != "second" {
		t.Fatalf("load = %q, want %q", got, "second")
	}
	if err := Delete(account); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if v, err := Load(account); err != nil || v != "" {
		t.Fatalf("load after delete = %q, %v", v, err)
	}
}
