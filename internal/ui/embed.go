// Package ui embeds HTML templates and static assets.
package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
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
		"formatBytes": func(n int64) string {
			if n < 1024 {
				return fmt.Sprintf("%d B", n)
			}
			if n < 1024*1024 {
				return fmt.Sprintf("%.1f KiB", float64(n)/1024)
			}
			if n < 1024*1024*1024 {
				return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
			}
			return fmt.Sprintf("%.2f GiB", float64(n)/(1024*1024*1024))
		},
		"lower": strings.ToLower,
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
