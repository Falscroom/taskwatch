// Package frontend — окно taskwatch: HTML, CSS и ES-модули в js/ без сборщика,
// встроенные в бинарь. Тесты чистых модулей — в test/, запускаются node (make test-frontend).
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.css js
var files embed.FS

// FS — файлы окна.
func FS() fs.FS { return files }
