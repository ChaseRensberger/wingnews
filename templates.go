package main

import (
	"bytes"
	"html/template"
	"net/http"
)

func newServer() *server {
	tmpl := template.Must(template.New("all").Funcs(template.FuncMap{
		"inc": func(v int) int { return v + 1 },
		"dec": func(v int) int { return v - 1 },
		"mul": func(a, b int) int { return a * b },
	}).ParseFiles(
		"index.html",
		"templates/feed.html",
		"templates/item.html",
		"templates/comments.html",
		"templates/user.html",
		"templates/submit.html",
	))

	return &server{
		hn:   newHNClient(),
		tmpl: tmpl,
	}
}

func (s *server) render(w http.ResponseWriter, r *http.Request, active, title, bodyTemplate string, data any) {
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, bodyTemplate, data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHXRequest(r) {
		_, _ = w.Write(body.Bytes())
		return
	}

	layout := layoutData{
		Title:   title,
		Active:  active,
		Content: template.HTML(body.String()),
	}
	if err := s.tmpl.ExecuteTemplate(w, "layout", layout); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}
