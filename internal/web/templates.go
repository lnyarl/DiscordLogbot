package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
)

//go:embed templates/*.html
var embeddedTemplates embed.FS

// PageData is the union of fields any template might reference. Mirrors
// Python's `tpl.env.globals` (bot_invite_url) plus per-request username.
type PageData struct {
	Username     string
	BotInviteURL string
}

// Templates owns the parsed template set: a base.html + every page that
// extends it. Same Jinja inheritance pattern translated to Go's "block"
// directive.
type Templates struct {
	pages map[string]*template.Template // name → fully-resolved tree
}

// LoadTemplates parses the embedded base.html with each page partial.
func LoadTemplates() (*Templates, error) {
	pages := []string{"login.html", "search.html"}
	out := &Templates{pages: make(map[string]*template.Template, len(pages))}
	for _, p := range pages {
		t, err := template.ParseFS(embeddedTemplates, "templates/base.html", "templates/"+p)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		out.pages[p] = t
	}
	return out, nil
}

// Render writes the template named `page` (e.g. "login.html") to w using
// PageData for the inheritance chain. Errors are logged + 500 to the
// caller — same as Jinja's TemplateResponse on failure.
func (t *Templates) Render(w http.ResponseWriter, page string, data PageData) {
	tpl, ok := t.pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Execute base.html (the layout) — its blocks pull in the page's
	// {{define "title"}} / {{define "content"}}.
	if err := tpl.ExecuteTemplate(w, "base.html", data); err != nil {
		slog.Error("template render failed", "page", page, "err", err)
	}
}

// RenderTo is for tests: write to any io.Writer.
func (t *Templates) RenderTo(w io.Writer, page string, data PageData) error {
	tpl, ok := t.pages[page]
	if !ok {
		return fmt.Errorf("template not found: %s", page)
	}
	return tpl.ExecuteTemplate(w, "base.html", data)
}
