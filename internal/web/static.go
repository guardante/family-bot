package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticEmbed embed.FS

// StaticFS возвращает содержимое папки static как корень файловой системы.
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticEmbed, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
