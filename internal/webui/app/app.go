// Package app embeds the new React admin dashboard (Vite build output —
// see /web) directly into the onebox binary via go:embed, same
// single-binary-deployment property webui.Handler already has for the
// vanilla dashboard it's incrementally replacing (see ARCHITECTURE.md's
// Milestone 2 note). Run `npm run build` in /web before `go build` here;
// dist/ is checked in with a placeholder so a fresh clone still compiles
// even before that's ever been run.
package app

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var files embed.FS

// Handler serves the React app's built assets, with SPA fallback: any
// request whose path doesn't match a real file under dist/ (a
// client-side route like /app/collections/orders, not a missing asset)
// gets index.html instead of a 404, so React Router owns navigation for
// everything under the mount point — the same rewrite an Nginx/Vercel
// static-SPA config would apply, done here in Go since onebox has no
// separate web server in front of it.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic("app: dist missing from embed: " + err.Error())
	}
	fsys := http.FS(sub)
	fileServer := http.FileServer(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		if clean != "/" {
			if f, err := fsys.Open(strings.TrimPrefix(clean, "/")); err != nil {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, r2)
				return
			} else {
				f.Close()
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
