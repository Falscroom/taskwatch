package tray

import (
	"bytes"
	"image/png"
	"testing"
)

func TestTrayIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayIcon()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 36 || b.Dy() != 36 {
		t.Errorf("icon size = %v, want 36×36", b)
	}
}
