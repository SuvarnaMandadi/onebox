// Package webui embeds the admin dashboard's static assets (plain
// HTML/CSS/JS, no build step) directly into the onebox binary via
// go:embed, keeping the single-binary promise intact.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

// Handler serves the dashboard's static assets rooted at "/", suitable
// for mounting under a prefix like /_/.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic("webui: static assets missing from embed: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}
