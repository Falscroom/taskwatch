package keychain

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Roundtrip на временной записи: до реальных токенов не касается, но лезет
// в настоящую Связку ключей — поэтому только по явному TASKWATCH_KEYCHAIN_TEST=1.
func TestSaveLoadDelete(t *testing.T) {
	if os.Getenv("TASKWATCH_KEYCHAIN_TEST") == "" {
		t.Skip("set TASKWATCH_KEYCHAIN_TEST=1 to run against the real Keychain")
	}
	const account = "test-roundtrip"
	t.Cleanup(func() { _ = Delete(account) }) // уборка даже при падении

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

// Ошибки: текст от macOS вместо голого кода, отказы в доступе — ErrAccessDenied.
// Связку ключей не трогает: текст ошибки по коду выдаёт сам Security.framework.
func TestStatusError(t *testing.T) {
	for _, code := range []int32{errSecUserCanceled, errSecAuthFailed, errSecInteractionNotAllowed, errAuthorizationInternal} {
		if err := statusError("load", code); !errors.Is(err, ErrAccessDenied) {
			t.Errorf("code %d: %v must be ErrAccessDenied", code, err)
		}
	}

	err := statusError("add", errSecDuplicateItem)
	if errors.Is(err, ErrAccessDenied) {
		t.Errorf("duplicate is not a denial: %v", err)
	}
	msg := err.Error()
	t.Logf("example: %s; denied: %v", msg, statusError("load", errSecAuthFailed))
	if !strings.HasPrefix(msg, "keychain add: ") || !strings.HasSuffix(msg, "(osstatus -25299)") || len(msg) <= len("keychain add: (osstatus -25299)") {
		t.Errorf("want macOS text and the code: %q", msg)
	}

	// Коду без текста хватает самого кода.
	if got := statusError("load", 123456).Error(); got != "keychain load: osstatus 123456" && !strings.HasSuffix(got, "(osstatus 123456)") {
		t.Errorf("unknown code: %q", got)
	}
}
