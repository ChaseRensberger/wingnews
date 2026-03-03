package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	comments, commentsErr := s.loadComments(r.Context(), item.ID, item.Kids, sortMode)

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
	comments, commentsErr := s.loadComments(r.Context(), item.ID, item.Kids, sortMode)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "comments", map[string]any{
		"Comments":      comments,
		"Sort":          sortMode,
		"CommentsError": commentsErr,
	}); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *server) handleCommentChildren(w http.ResponseWriter, r *http.Request) {
	storyID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	commentID, err := strconv.Atoi(chi.URLParam(r, "commentID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	parentDepth := parsePositiveInt(r.URL.Query().Get("depth"), 1)
	sortMode := parseSort(r.URL.Query().Get("sort"))

	children, childrenErr := s.loadCommentChildren(r.Context(), storyID, commentID, parentDepth, sortMode)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "commentChildren", map[string]any{
		"Comments":      children,
		"CommentsError": childrenErr,
	}); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *server) loadComments(ctx context.Context, storyID int, ids []int, sortMode string) ([]*commentView, string) {
	cacheKey := fmt.Sprintf("comment-tree:%d:%s", storyID, sortMode)
	value, err := s.commentsCache.getOrLoad(ctx, cacheKey, 45*time.Second, func(ctx context.Context) (any, error) {
		comments, commentsErr := s.loadCommentsFresh(ctx, storyID, ids, sortMode, 0)
		return struct {
			Comments []*commentView
			Error    string
		}{Comments: comments, Error: commentsErr}, nil
	})
	if err != nil {
		return nil, "Unable to load comments right now."
	}

	payload, _ := value.(struct {
		Comments []*commentView
		Error    string
	})
	return payload.Comments, payload.Error
}

func (s *server) loadCommentChildren(ctx context.Context, storyID, commentID, parentDepth int, sortMode string) ([]*commentView, string) {
	cacheKey := fmt.Sprintf("comment-children:%d:%d:%d:%s", storyID, commentID, parentDepth, sortMode)
	value, err := s.commentsCache.getOrLoad(ctx, cacheKey, 45*time.Second, func(ctx context.Context) (any, error) {
		parent, err := s.hn.getItem(ctx, commentID)
		if err != nil || parent == nil {
			return struct {
				Comments []*commentView
				Error    string
			}{Error: "Unable to load replies right now."}, nil
		}

		children, childrenErr := s.loadCommentsFresh(ctx, storyID, parent.Kids, sortMode, parentDepth)
		return struct {
			Comments []*commentView
			Error    string
		}{Comments: children, Error: childrenErr}, nil
	})
	if err != nil {
		return nil, "Unable to load replies right now."
	}

	payload, _ := value.(struct {
		Comments []*commentView
		Error    string
	})
	return payload.Comments, payload.Error
}

func (s *server) loadCommentsFresh(ctx context.Context, storyID int, ids []int, sortMode string, baseDepth int) ([]*commentView, string) {
	const maxNodes = 200
	const eagerDepth = 2

	itemsByID, truncated := s.fetchCommentItems(ctx, ids, maxNodes)
	comments := s.buildCommentTree(ids, itemsByID, baseDepth, baseDepth+eagerDepth, storyID, sortMode)
	if sortMode == "new" {
		sortCommentsByNewest(comments)
	}

	if truncated {
		return comments, "Some comments were deferred for faster rendering."
	}
	return comments, ""
}

func (s *server) fetchCommentItems(ctx context.Context, ids []int, maxNodes int) (map[int]*hnItem, bool) {
	if len(ids) == 0 || maxNodes <= 0 {
		return map[int]*hnItem{}, false
	}

	workers := 24
	if maxNodes < workers {
		workers = maxNodes
	}

	itemsByID := make(map[int]*hnItem, maxNodes)
	seen := make(map[int]struct{}, maxNodes)
	jobs := make(chan int, workers*2)
	done := make(chan struct{})
	var doneOnce sync.Once

	var mu sync.Mutex
	var workerWG sync.WaitGroup

	scheduled := 0
	truncated := false
	pending := int64(0)

	enqueue := func(id int) {
		if id <= 0 {
			return
		}

		mu.Lock()
		if _, exists := seen[id]; exists {
			mu.Unlock()
			return
		}
		if scheduled >= maxNodes {
			truncated = true
			mu.Unlock()
			return
		}
		seen[id] = struct{}{}
		scheduled++
		mu.Unlock()

		atomic.AddInt64(&pending, 1)

		select {
		case <-ctx.Done():
			if atomic.AddInt64(&pending, -1) == 0 {
				doneOnce.Do(func() { close(done) })
			}
		case jobs <- id:
		}
	}

	for range workers {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for id := range jobs {
				item, err := s.hn.getItem(ctx, id)
				if err == nil && item != nil && item.Type == "comment" {
					mu.Lock()
					itemsByID[id] = item
					mu.Unlock()

					for _, kidID := range item.Kids {
						enqueue(kidID)
					}
				}
				if atomic.AddInt64(&pending, -1) == 0 {
					doneOnce.Do(func() { close(done) })
				}
			}
		}()
	}

	for _, id := range ids {
		enqueue(id)
	}
	if atomic.LoadInt64(&pending) == 0 {
		doneOnce.Do(func() { close(done) })
	}

	select {
	case <-done:
	case <-ctx.Done():
	}
	close(jobs)
	workerWG.Wait()

	if ctx.Err() != nil {
		truncated = true
	}

	return itemsByID, truncated
}

func (s *server) buildCommentTree(ids []int, itemsByID map[int]*hnItem, depth, depthLimit, storyID int, sortMode string) []*commentView {
	if len(ids) == 0 {
		return nil
	}

	out := make([]*commentView, 0, len(ids))
	for _, id := range ids {
		item, ok := itemsByID[id]
		if !ok || item == nil || item.Type != "comment" {
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

		if len(item.Kids) > 0 {
			if depth+1 >= depthLimit {
				node.HasMoreChildren = true
				node.HiddenChildren = len(item.Kids)
				node.ExpandPath = fmt.Sprintf("/item/%d/comment/%d/children?sort=%s&depth=%d", storyID, item.ID, sortMode, depth+1)
			} else {
				node.Children = s.buildCommentTree(item.Kids, itemsByID, depth+1, depthLimit, storyID, sortMode)
				if len(node.Children) < len(item.Kids) {
					node.HasMoreChildren = true
					node.HiddenChildren = len(item.Kids) - len(node.Children)
					node.ExpandPath = fmt.Sprintf("/item/%d/comment/%d/children?sort=%s&depth=%d", storyID, item.ID, sortMode, depth+1)
				}
			}
		}

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
