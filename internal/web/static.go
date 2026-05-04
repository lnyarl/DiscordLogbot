package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// StaticDirs bundles the three on-disk directories the web server exposes.
type StaticDirs struct {
	Static      string // /static/* (favicon.svg, style.css, etc.)
	Attachments string // /attachments/* (bot-downloaded message attachments)
	Emojis      string // /emojis/* (bot-downloaded custom emojis)
}

// CheckExists mirrors web/main.py's startup guard: every configured
// directory must exist before the server starts (otherwise the bot
// container probably hasn't booted yet and links would 404 silently).
func (d StaticDirs) CheckExists() error {
	for _, dir := range []string{d.Static, d.Attachments, d.Emojis} {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("디렉토리가 존재하지 않습니다: %s\nbot 컨테이너가 먼저 기동되어야 합니다", dir)
			}
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("not a directory: %s", dir)
		}
	}
	return nil
}

// Mount wires the three static prefixes onto mux. Each prefix maps to a
// FileServer rooted at the configured directory; the Static dir is
// optional (server falls through to next handler if not configured).
func (d StaticDirs) Mount(mux *http.ServeMux) {
	if d.Static != "" {
		mux.Handle("GET /static/", http.StripPrefix("/static/", noDirListingFS(d.Static)))
	}
	if d.Attachments != "" {
		mux.Handle("GET /attachments/", http.StripPrefix("/attachments/", noDirListingFS(d.Attachments)))
	}
	if d.Emojis != "" {
		mux.Handle("GET /emojis/", http.StripPrefix("/emojis/", noDirListingFS(d.Emojis)))
	}
}

// noDirListingFS serves files from root but returns 404 for directory
// requests (the default http.FileServer would render an index, which
// we don't want to leak the attachment store layout).
func noDirListingFS(root string) http.Handler {
	root = filepath.Clean(root)
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject any path containing ".." after cleaning; http.Dir already
		// rejects these but we're explicit.
		clean := filepath.Clean(r.URL.Path)
		if clean == "." || hasDirSuffix(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func hasDirSuffix(p string) bool {
	return len(p) > 0 && p[len(p)-1] == '/'
}
