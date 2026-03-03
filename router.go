package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

func newRouter(s *server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(8 * time.Second))
	r.Use(httprate.LimitByRealIP(100, time.Minute))

	r.Get("/robots.txt", handleRobotsTxt)

	r.Handle("/output.css", http.FileServer(http.Dir(".")))
	r.Handle("/public/*", http.StripPrefix("/public/", http.FileServer(http.Dir("public"))))
	r.Get("/", s.handleFeed("top", "/", "Top Stories"))
	r.Get("/new", s.handleFeed("new", "/new", "New Stories"))
	r.Get("/best", s.handleFeed("best", "/best", "Best Stories"))
	r.Get("/ask", s.handleFeed("ask", "/ask", "Ask HN"))
	r.Get("/show", s.handleFeed("show", "/show", "Show HN"))
	r.Get("/jobs", s.handleFeed("jobs", "/jobs", "Jobs"))
	r.Get("/item/{id}", s.handleItem)
	r.Get("/item/{id}/comments", s.handleItemComments)
	r.Get("/item/{id}/comment/{commentID}/children", s.handleCommentChildren)
	r.Get("/user/{id}", s.handleUser)
	r.Get("/submit", s.handleSubmit)

	return r
}

const robotsTxt = `User-agent: *
Crawl-delay: 10
Disallow: /item/
`

func handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(robotsTxt))
}
