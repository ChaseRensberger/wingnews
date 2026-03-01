package main

import (
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *server) handleFeed(feed, path, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := parsePositiveInt(r.URL.Query().Get("page"), 1)

		ids, err := s.hn.getFeedIDs(r.Context(), feed)
		if err != nil {
			s.render(w, r, feed, title, "feed", feedPageData{
				Feed:  feed,
				Path:  path,
				Page:  page,
				Error: "Hacker News is not responding right now. Showing cached results when available.",
			})
			return
		}

		start := (page - 1) * pageSize
		if start >= len(ids) {
			start = 0
			page = 1
		}
		end := start + pageSize
		if end > len(ids) {
			end = len(ids)
		}

		items := s.hn.hydrateStories(r.Context(), ids[start:end])
		stories := make([]storyView, 0, len(items))
		rank := start + 1
		for _, item := range items {
			if item == nil || item.Deleted || item.Dead {
				rank++
				continue
			}

			view := toStoryView(item, rank)
			stories = append(stories, view)
			rank++
		}

		s.render(w, r, feed, title, "feed", feedPageData{
			Feed:    feed,
			Path:    path,
			Page:    page,
			HasMore: end < len(ids),
			Stories: stories,
		})
	}
}

func (s *server) handleItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	item, err := s.hn.getItem(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sortMode := parseSort(r.URL.Query().Get("sort"))
	comments, commentsErr := s.loadComments(r.Context(), item.Kids, sortMode)

	data := itemPageData{
		Story:         toStoryView(item, 0),
		Text:          template.HTML(item.Text),
		Comments:      comments,
		Sort:          sortMode,
		CommentsError: commentsErr,
		TotalComments: len(comments),
	}

	s.render(w, r, "", item.Title, "item", data)
}

func (s *server) handleItemComments(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	item, err := s.hn.getItem(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sortMode := parseSort(r.URL.Query().Get("sort"))
	comments, commentsErr := s.loadComments(r.Context(), item.Kids, sortMode)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "comments", map[string]any{
		"Comments":      comments,
		"Sort":          sortMode,
		"CommentsError": commentsErr,
	}); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *server) loadComments(ctx context.Context, ids []int, sortMode string) ([]*commentView, string) {
	remaining := 350
	comments := s.buildCommentTree(ctx, ids, 0, &remaining)
	if sortMode == "new" {
		sortCommentsByNewest(comments)
	}

	if remaining == 0 {
		return comments, "Comment tree is truncated for faster rendering."
	}
	return comments, ""
}

func (s *server) buildCommentTree(ctx context.Context, ids []int, depth int, remaining *int) []*commentView {
	if len(ids) == 0 || *remaining <= 0 {
		return nil
	}

	out := make([]*commentView, 0, len(ids))
	for _, id := range ids {
		if *remaining <= 0 {
			break
		}
		*remaining--

		item, err := s.hn.getItem(ctx, id)
		if err != nil || item.Type != "comment" {
			continue
		}

		node := &commentView{
			ID:      item.ID,
			By:      fallback(item.By, "unknown"),
			Created: item.Time,
			TimeAgo: timeAgo(item.Time),
			Text:    template.HTML(item.Text),
			Depth:   depth,
			Deleted: item.Deleted,
			Dead:    item.Dead,
		}
		node.Children = s.buildCommentTree(ctx, item.Kids, depth+1, remaining)
		out = append(out, node)
	}

	return out
}

func (s *server) handleUser(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		http.NotFound(w, r)
		return
	}

	user, err := s.hn.getUser(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	limit := 20
	if len(user.Submitted) < limit {
		limit = len(user.Submitted)
	}

	items := s.hn.hydrateStories(r.Context(), user.Submitted[:limit])
	submitted := make([]storyView, 0, limit)
	for _, item := range items {
		if item == nil {
			continue
		}
		submitted = append(submitted, toStoryView(item, 0))
	}

	s.render(w, r, "", "user: "+user.ID, "user", userPageData{
		ID:         user.ID,
		Karma:      user.Karma,
		CreatedAgo: timeAgo(user.Created),
		About:      template.HTML(user.About),
		Submitted:  submitted,
	})
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "submit", "Submit", "submit", nil)
}
