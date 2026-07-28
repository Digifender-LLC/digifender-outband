// Package ui embeds HTML templates and static assets.
package ui

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// ParseTemplates loads all page and partial templates.
func ParseTemplates() (*template.Template, error) {
	funcMap := template.FuncMap{
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.UTC().Format("2006-01-02 15:04:05 UTC")
		},
		"powerLabel": func(on bool) string {
			if on {
				return "ON"
			}
			return "OFF"
		},
	}
	return template.New("").Funcs(funcMap).ParseFS(templateFS,
		"templates/*.html",
		"templates/partials/*.html",
	)
}

// StaticHandler serves vendored CSS/JS under /static/.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
