package loginitem

import "testing"

// Тестовый бинарь лежит не в бандле: macOS его не находит, и управлять объектом
// входа нельзя. Set здесь не вызываем — он поменял бы настройки системы.
func TestOutsideBundle(t *testing.T) {
	if got := Current(); got != NotFound {
		t.Fatalf("status outside a bundle = %d, want NotFound", got)
	}
	if (Item{}).Supported() || (Item{}).Enabled() {
		t.Error("outside a bundle the login item is neither supported nor enabled")
	}
}
