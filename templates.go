package main

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
)

type seoData struct {
	Description  string
	CanonicalURL string
	OGType       string
	JSONLD       template.HTML
}

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
		hn:            newHNClient(),
		algolia:       newAlgoliaClient(),
		github:        newGitHubClient(githubRepo),
		tmpl:          tmpl,
		commentsCache: newCommentsMemoryCache(),
	}
}

func (s *server) render(w http.ResponseWriter, r *http.Request, active, title, bodyTemplate string, data any, seo ...seoData) {
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, bodyTemplate, data); err != nil {
		slog.Error("template render failed", append(requestAttrs(r), "template", bodyTemplate, "error", err)...)
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHXRequest(r) {
		w.Header().Set("Cache-Control", "public, max-age=30, stale-while-revalidate=60")
		_, _ = w.Write(body.Bytes())
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=120")

	layout := layoutData{
		Title:     title,
		Active:    active,
		Content:   template.HTML(body.String()),
		GitHubURL: "https://github.com/" + githubRepo,
	}
	if len(seo) > 0 {
		layout.Description = seo[0].Description
		layout.CanonicalURL = seo[0].CanonicalURL
		layout.OGType = seo[0].OGType
		layout.JSONLD = seo[0].JSONLD
	}
	if stars, err := s.github.getRepoStars(r.Context()); err == nil {
		layout.GitHubStars = formatStarCount(stars)
	}
	if err := s.tmpl.ExecuteTemplate(w, "layout", layout); err != nil {
		slog.Error("template render failed", append(requestAttrs(r), "template", "layout", "error", err)...)
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}
