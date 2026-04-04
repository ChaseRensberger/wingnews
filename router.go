package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

func newRouter(s *server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5, "text/html", "text/css", "application/javascript", "text/plain", "application/xml"))

	// Static assets: long cache + no rate limiting
	r.Group(func(r chi.Router) {
		r.Use(staticCacheControl)
		r.Handle("/output.css", http.FileServer(http.Dir(".")))
		r.Handle("/public/*", http.StripPrefix("/public/", http.FileServer(http.Dir("public"))))
	})

	// Dynamic routes: rate limited + request timeout
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(8 * time.Second))
		r.Use(httprate.LimitByRealIP(100, time.Minute))

		r.Get("/robots.txt", handleRobotsTxt)
		r.Get("/sitemap.xml", handleSitemap)
		r.Get("/", s.handleFeed("top", "/", "Top Stories"))
		r.Get("/new", s.handleFeed("new", "/new", "New Stories"))
		r.Get("/best", s.handleFeed("best", "/best", "Best Stories"))
		r.Get("/ask", s.handleFeed("ask", "/ask", "Ask HN"))
		r.Get("/show", s.handleFeed("show", "/show", "Show HN"))
		r.Get("/jobs", s.handleFeed("jobs", "/jobs", "Jobs"))
		r.Get("/submit", s.handleSubmit)
	})

	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitByRealIP(20, time.Minute))
		r.Get("/item/{id}", s.handleItem)
		r.Get("/item/{id}/comments", s.handleItemComments)
		r.Get("/item/{id}/more-comments", s.handleMoreComments)
		r.Get("/item/{id}/comment/{commentID}/children", s.handleCommentChildren)
		r.Get("/user/{id}", s.handleUser)
		r.Get("/user/{id}/submissions", s.handleUserSubmissions)
		r.Get("/user/{id}/comments", s.handleUserComments)
	})

	return r
}

// staticCacheControl sets long-lived Cache-Control headers for static assets.
func staticCacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		} else if strings.HasSuffix(r.URL.Path, ".png") || strings.HasSuffix(r.URL.Path, ".webp") || strings.HasSuffix(r.URL.Path, ".ico") {
			w.Header().Set("Cache-Control", "public, max-age=604800")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}

const robotsTxt = `User-agent: *
Disallow: /item/
Disallow: /user/
Crawl-delay: 10
Sitemap: https://news.wingman.actor/sitemap.xml
`

func handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(robotsTxt))
}

type sitemapURL struct {
	Loc        string
	ChangeFreq string
	Priority   string
}

func handleSitemap(w http.ResponseWriter, r *http.Request) {
	urls := []sitemapURL{
		{Loc: baseURL + "/", ChangeFreq: "hourly", Priority: "1.0"},
		{Loc: baseURL + "/new", ChangeFreq: "hourly", Priority: "0.8"},
		{Loc: baseURL + "/best", ChangeFreq: "daily", Priority: "0.8"},
		{Loc: baseURL + "/ask", ChangeFreq: "hourly", Priority: "0.8"},
		{Loc: baseURL + "/show", ChangeFreq: "hourly", Priority: "0.8"},
		{Loc: baseURL + "/jobs", ChangeFreq: "daily", Priority: "0.7"},
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, "\n")
	fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	fmt.Fprint(w, "\n")
	for _, u := range urls {
		fmt.Fprintf(w, "  <url>\n    <loc>%s</loc>\n    <changefreq>%s</changefreq>\n    <priority>%s</priority>\n  </url>\n",
			u.Loc, u.ChangeFreq, u.Priority)
	}
	fmt.Fprint(w, `</urlset>`)
	fmt.Fprint(w, "\n")
}
