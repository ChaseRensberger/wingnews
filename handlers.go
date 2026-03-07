package main

import (
	"context"
	"encoding/json"
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
	descriptions := map[string]string{
		"top":  "Browse the top stories on Hacker News. A fast, clean reader for the best links and discussions.",
		"new":  "The newest stories submitted to Hacker News, updated in real time.",
		"best": "The best stories on Hacker News — highly upvoted links and discussions.",
		"ask":  "Ask HN discussions: questions, advice, and community threads from Hacker News.",
		"show": "Show HN projects and launches: things people are building and sharing with the community.",
		"jobs": "Job listings posted to Hacker News by startups and tech companies.",
	}

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

		canonical := baseURL + path
		if page > 1 {
			canonical = fmt.Sprintf("%s%s?page=%d", baseURL, path, page)
		}

		s.render(w, r, feed, title, "feed", feedPageData{
			Feed:    feed,
			Path:    path,
			Page:    page,
			HasMore: end < len(ids),
			Stories: stories,
		}, seoData{
			Description:  descriptions[feed],
			CanonicalURL: canonical,
			OGType:       "website",
		})
	}
}

const topLevelPageSize = 20

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
	pageIDs, remaining := paginateIDs(item.Kids, 0, topLevelPageSize)
	comments, commentsErr := s.loadComments(r.Context(), item.ID, pageIDs, sortMode, 0)

	data := itemPageData{
		Story:         toStoryView(item, 0),
		Text:          template.HTML(item.Text),
		Comments:      comments,
		Sort:          sortMode,
		CommentsError: commentsErr,
		TotalComments: len(comments),
	}
	if remaining > 0 {
		data.HasMoreComments = true
		data.RemainingComments = remaining
		data.LoadMorePath = fmt.Sprintf("/item/%d/more-comments?sort=%s&offset=%d", item.ID, sortMode, topLevelPageSize)
	}

	description := buildItemDescription(item)
	jsonld := buildItemJSONLD(item)
	canonicalURL := fmt.Sprintf("%s/item/%d", baseURL, item.ID)

	s.render(w, r, "", item.Title, "item", data, seoData{
		Description:  description,
		CanonicalURL: canonicalURL,
		OGType:       "article",
		JSONLD:       jsonld,
	})
}

// buildItemDescription constructs a short meta description for a story page.
func buildItemDescription(item *hnItem) string {
	parts := []string{}
	if item.Score > 0 {
		parts = append(parts, fmt.Sprintf("%d points", item.Score))
	}
	if item.By != "" {
		parts = append(parts, "by "+item.By)
	}
	if item.Descendants > 0 {
		parts = append(parts, fmt.Sprintf("%d comments", item.Descendants))
	}
	domain := domainFromURL(item.URL)
	if domain != "" && domain != "news.ycombinator.com" {
		parts = append(parts, "from "+domain)
	}
	if len(parts) == 0 {
		return "Discussion on Hacker News via WingNews."
	}
	return strings.Join(parts, " · ") + " — via WingNews."
}

// buildItemJSONLD generates a DiscussionForumPosting JSON-LD block for a story.
func buildItemJSONLD(item *hnItem) template.HTML {
	type interactionCounter struct {
		Type             string `json:"@type"`
		InteractionType  string `json:"interactionType"`
		UserInteractions int    `json:"userInteractionCount"`
	}
	type author struct {
		Type string `json:"@type"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	type jsonldDoc struct {
		Context          string               `json:"@context"`
		Type             string               `json:"@type"`
		Headline         string               `json:"headline"`
		URL              string               `json:"url"`
		Author           author               `json:"author"`
		DatePublished    string               `json:"datePublished"`
		InteractionStats []interactionCounter `json:"interactionStatistic,omitempty"`
		IsPartOf         map[string]string    `json:"isPartOf,omitempty"`
	}

	doc := jsonldDoc{
		Context:       "https://schema.org",
		Type:          "DiscussionForumPosting",
		Headline:      item.Title,
		URL:           fmt.Sprintf("%s/item/%d", baseURL, item.ID),
		DatePublished: time.Unix(item.Time, 0).UTC().Format(time.RFC3339),
		Author: author{
			Type: "Person",
			Name: fallback(item.By, "unknown"),
			URL:  fmt.Sprintf("%s/user/%s", baseURL, item.By),
		},
		IsPartOf: map[string]string{
			"@type": "WebSite",
			"name":  "WingNews",
			"url":   baseURL,
		},
	}
	if item.Score > 0 {
		doc.InteractionStats = append(doc.InteractionStats, interactionCounter{
			Type:             "InteractionCounter",
			InteractionType:  "https://schema.org/LikeAction",
			UserInteractions: item.Score,
		})
	}
	if item.Descendants > 0 {
		doc.InteractionStats = append(doc.InteractionStats, interactionCounter{
			Type:             "InteractionCounter",
			InteractionType:  "https://schema.org/CommentAction",
			UserInteractions: item.Descendants,
		})
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return template.HTML(b)
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
	pageIDs, remaining := paginateIDs(item.Kids, 0, topLevelPageSize)
	comments, commentsErr := s.loadComments(r.Context(), item.ID, pageIDs, sortMode, 0)

	data := map[string]any{
		"Comments":      comments,
		"Sort":          sortMode,
		"CommentsError": commentsErr,
	}
	if remaining > 0 {
		data["HasMoreComments"] = true
		data["RemainingComments"] = remaining
		data["LoadMorePath"] = fmt.Sprintf("/item/%d/more-comments?sort=%s&offset=%d", item.ID, sortMode, topLevelPageSize)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "comments", data); err != nil {
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

func (s *server) handleMoreComments(w http.ResponseWriter, r *http.Request) {
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

	offset := parsePositiveInt(r.URL.Query().Get("offset"), 0)
	sortMode := parseSort(r.URL.Query().Get("sort"))
	pageIDs, remaining := paginateIDs(item.Kids, offset, topLevelPageSize)
	comments, commentsErr := s.loadComments(r.Context(), item.ID, pageIDs, sortMode, offset)

	data := map[string]any{
		"Comments":      comments,
		"CommentsError": commentsErr,
	}
	if remaining > 0 {
		data["HasMoreComments"] = true
		data["RemainingComments"] = remaining
		data["LoadMorePath"] = fmt.Sprintf("/item/%d/more-comments?sort=%s&offset=%d", item.ID, sortMode, offset+topLevelPageSize)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "moreComments", data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *server) loadComments(ctx context.Context, storyID int, ids []int, sortMode string, offset int) ([]*commentView, string) {
	cacheKey := fmt.Sprintf("comment-tree:%d:%s:%d", storyID, sortMode, offset)
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
	const maxNodes = 50
	const eagerDepth = 2

	itemsByID, _ := s.fetchCommentItems(ctx, ids, maxNodes)
	comments := s.buildCommentTree(ids, itemsByID, baseDepth, baseDepth+eagerDepth, storyID, sortMode)
	if sortMode == "new" {
		sortCommentsByNewest(comments)
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
			StoryID: storyID,
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

	jsonld := buildUserJSONLD(user)

	s.render(w, r, "", "user: "+user.ID, "user", userPageData{
		ID:         user.ID,
		Karma:      user.Karma,
		CreatedAgo: timeAgo(user.Created),
		About:      template.HTML(user.About),
	}, seoData{
		Description:  fmt.Sprintf("%s on Hacker News — %d karma, joined %s. View their submissions and comments on WingNews.", user.ID, user.Karma, timeAgo(user.Created)),
		CanonicalURL: fmt.Sprintf("%s/user/%s", baseURL, user.ID),
		OGType:       "profile",
		JSONLD:       jsonld,
	})
}

// buildUserJSONLD generates a ProfilePage JSON-LD block for a user page.
func buildUserJSONLD(user *hnUser) template.HTML {
	type person struct {
		Type        string `json:"@type"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description,omitempty"`
	}
	type jsonldDoc struct {
		Context    string `json:"@context"`
		Type       string `json:"@type"`
		MainEntity person `json:"mainEntity"`
		URL        string `json:"url"`
	}

	doc := jsonldDoc{
		Context: "https://schema.org",
		Type:    "ProfilePage",
		URL:     fmt.Sprintf("%s/user/%s", baseURL, user.ID),
		MainEntity: person{
			Type: "Person",
			Name: user.ID,
			URL:  fmt.Sprintf("%s/user/%s", baseURL, user.ID),
		},
	}
	if user.About != "" {
		doc.MainEntity.Description = user.About
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return template.HTML(b)
}

const userPageSize = 20

// filterSubmittedIDs hydrates items from the user's Submitted list in batches,
// filtering by type. It skips items that don't match the filter and continues
// fetching until it has enough items or runs out of IDs.
func (s *server) filterSubmittedIDs(ctx context.Context, submitted []int, offset int, limit int, keep func(*hnItem) bool) ([]*hnItem, bool) {
	const batchSize = 50
	result := make([]*hnItem, 0, limit)
	pos := offset

	for len(result) < limit && pos < len(submitted) {
		end := pos + batchSize
		if end > len(submitted) {
			end = len(submitted)
		}

		items := s.hn.hydrateStories(ctx, submitted[pos:end])
		for _, item := range items {
			if item == nil || item.Deleted || item.Dead {
				continue
			}
			if keep(item) {
				result = append(result, item)
				if len(result) >= limit {
					break
				}
			}
		}
		pos = end
	}

	hasMore := pos < len(submitted) || len(result) > limit
	if len(result) > limit {
		result = result[:limit]
	}

	return result, hasMore
}

func (s *server) handleUserSubmissions(w http.ResponseWriter, r *http.Request) {
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

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)

	// We need to scan through the submitted list to find stories.
	// Since the list mixes stories and comments, we can't use a simple offset.
	// Instead, we skip (page-1)*pageSize matching items.
	skip := (page - 1) * userPageSize
	needed := skip + userPageSize + 1 // +1 to check if there's a next page

	items, _ := s.filterSubmittedIDs(r.Context(), user.Submitted, 0, needed, func(item *hnItem) bool {
		return item.Type != "comment"
	})

	hasMore := len(items) > skip+userPageSize
	if skip >= len(items) {
		items = nil
	} else {
		end := skip + userPageSize
		if end > len(items) {
			end = len(items)
		}
		items = items[skip:end]
	}

	stories := make([]storyView, 0, len(items))
	for _, item := range items {
		stories = append(stories, toStoryView(item, 0))
	}

	s.render(w, r, "", user.ID+"'s submissions", "userSubmissions", userSubmissionsPageData{
		UserID:  user.ID,
		Stories: stories,
		Page:    page,
		HasMore: hasMore,
	}, seoData{
		Description:  fmt.Sprintf("Stories submitted by %s on Hacker News, via WingNews.", user.ID),
		CanonicalURL: fmt.Sprintf("%s/user/%s/submissions", baseURL, user.ID),
		OGType:       "profile",
	})
}

func (s *server) handleUserComments(w http.ResponseWriter, r *http.Request) {
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

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)

	skip := (page - 1) * userPageSize
	needed := skip + userPageSize + 1

	items, _ := s.filterSubmittedIDs(r.Context(), user.Submitted, 0, needed, func(item *hnItem) bool {
		return item.Type == "comment"
	})

	hasMore := len(items) > skip+userPageSize
	if skip >= len(items) {
		items = nil
	} else {
		end := skip + userPageSize
		if end > len(items) {
			end = len(items)
		}
		items = items[skip:end]
	}

	// For each comment, find the root story for context ("on: Title")
	comments := make([]userCommentView, 0, len(items))
	var wg sync.WaitGroup
	type result struct {
		idx     int
		comment userCommentView
	}
	results := make(chan result, len(items))

	for i, item := range items {
		wg.Add(1)
		go func(idx int, item *hnItem) {
			defer wg.Done()
			cv := userCommentView{
				ID:      item.ID,
				By:      fallback(item.By, "unknown"),
				TimeAgo: timeAgo(item.Time),
				Text:    template.HTML(item.Text),
			}
			if root := s.hn.getRootStory(r.Context(), item); root != nil {
				cv.OnTitle = root.Title
				cv.OnID = root.ID
			}
			results <- result{idx: idx, comment: cv}
		}(i, item)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results preserving order
	ordered := make([]userCommentView, len(items))
	for res := range results {
		ordered[res.idx] = res.comment
	}
	for _, cv := range ordered {
		if cv.ID != 0 {
			comments = append(comments, cv)
		}
	}

	s.render(w, r, "", user.ID+"'s comments", "userComments", userCommentsPageData{
		UserID:   user.ID,
		Comments: comments,
		Page:     page,
		HasMore:  hasMore,
	}, seoData{
		Description:  fmt.Sprintf("Comments by %s on Hacker News, via WingNews.", user.ID),
		CanonicalURL: fmt.Sprintf("%s/user/%s/comments", baseURL, user.ID),
		OGType:       "profile",
	})
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "submit", "Submit", "submit", nil, seoData{
		Description:  "Submit a story or link to Hacker News.",
		CanonicalURL: baseURL + "/submit",
		OGType:       "website",
	})
}
