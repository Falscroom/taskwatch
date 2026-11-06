package browser

import (
	"slices"
	"testing"
)

func TestArgs(t *testing.T) {
	if got := args([]string{"https://gl/mr/1"}, false); !slices.Equal(got, []string{"https://gl/mr/1"}) {
		t.Errorf("foreground = %v", got)
	}
	if got := args([]string{"https://gl/mr/1", "https://gl/mr/2"}, true); !slices.Equal(got, []string{"-g", "https://gl/mr/1", "https://gl/mr/2"}) {
		t.Errorf("background = %v", got)
	}
}
