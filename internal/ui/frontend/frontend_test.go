package frontend

import (
	"io/fs"
	"testing"
)

func TestFilesEmbedded(t *testing.T) {
	for _, name := range []string{"index.html", "app.css", "app.js"} {
		data, err := fs.ReadFile(FS(), name)
		if err != nil || len(data) == 0 {
			t.Errorf("%s: %d bytes, %v", name, len(data), err)
		}
	}
}
