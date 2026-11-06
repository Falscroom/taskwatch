package browser

import (
	"slices"
	"testing"
)

func TestArgs(t *testing.T) {
	if got := args("https://gl/mr/1", false); !slices.Equal(got, []string{"https://gl/mr/1"}) {
		t.Errorf("foreground = %v", got)
	}
	if got := args("https://gl/mr/1", true); !slices.Equal(got, []string{"-g", "https://gl/mr/1"}) {
		t.Errorf("background = %v", got)
	}
}
