// Package frontend — окно taskwatch: HTML, CSS и JS без сборщика, встроенные в бинарь.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.css app.js
var files embed.FS

// FS — файлы окна.
func FS() fs.FS { return files }
