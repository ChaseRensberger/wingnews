package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
		slog.Error("template render failed", append(requestAttrs(r), "template", "comments", "error", err)...)
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
		slog.Error("template render failed", append(requestAttrs(r), "template", "commentChildren", "error", err)...)
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
		slog.Error("template render failed", append(requestAttrs(r), "template", "moreComments", "error", err)...)
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (s *server) loadComments(ctx context.Context, storyID int, ids []int, sortMode string, offset int) ([]*commentView, string) {
	cacheKey := fmt.Sprintf("comment-tree:%d:%s:%d", storyID, sortMode, offset)
	value, err := s.commentsCache.getOrLoad(ctx, cacheKey, ttlCommentTree, func(ctx context.Context) (any, error) {
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
	value, err := s.commentsCache.getOrLoad(ctx, cacheKey, ttlCommentTree, func(ctx context.Context) (any, error) {
		tree, err := s.algolia.getItemTree(ctx, storyID)
		if err != nil {
			return struct {
				Comments []*commentView
				Error    string
			}{Error: "Unable to load replies right now."}, nil
		}

		nodesByID := make(map[int]*algoliaItem)
		flattenAlgoliaTree(tree, nodesByID)

		parent, ok := nodesByID[commentID]
		if !ok || parent == nil {
			return struct {
				Comments []*commentView
				Error    string
			}{Error: "Unable to load replies right now."}, nil
		}

		childIDs := make([]int, len(parent.Children))
		for i, c := range parent.Children {
			childIDs[i] = c.ID
		}

		children := buildAlgoliaCommentTree(childIDs, nodesByID, parentDepth, storyID, sortMode)
		return struct {
			Comments []*commentView
			Error    string
		}{Comments: children}, nil
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

// loadCommentsFresh fetches the full comment tree for storyID from the Algolia API
// (a single HTTP request), then selects the top-level comments identified by ids
// and converts them to commentViews recursively.
func (s *server) loadCommentsFresh(ctx context.Context, storyID int, ids []int, sortMode string, baseDepth int) ([]*commentView, string) {
	tree, err := s.algolia.getItemTree(ctx, storyID)
	if err != nil {
		return nil, "Unable to load comments right now."
	}

	// Build a lookup map from the flat Algolia tree so we can find nodes by ID.
	nodesByID := make(map[int]*algoliaItem)
	flattenAlgoliaTree(tree, nodesByID)

	comments := buildAlgoliaCommentTree(ids, nodesByID, baseDepth, storyID, sortMode)
	if sortMode == "new" {
		sortCommentsByNewest(comments)
	}

	return comments, ""
}

// flattenAlgoliaTree walks the nested Algolia tree and populates a flat id→node map.
func flattenAlgoliaTree(node *algoliaItem, out map[int]*algoliaItem) {
	if node == nil {
		return
	}
	out[node.ID] = node
	for _, child := range node.Children {
		flattenAlgoliaTree(child, out)
	}
}

// buildAlgoliaCommentTree converts a slice of comment IDs into commentViews using
// the pre-fetched Algolia node map. Children are rendered recursively.
func buildAlgoliaCommentTree(ids []int, nodesByID map[int]*algoliaItem, depth, storyID int, sortMode string) []*commentView {
	if len(ids) == 0 {
		return nil
	}

	out := make([]*commentView, 0, len(ids))
	for _, id := range ids {
		node, ok := nodesByID[id]
		if !ok || node == nil || node.Type != "comment" {
			continue
		}

		cv := &commentView{
			ID:      node.ID,
			StoryID: storyID,
			By:      fallback(node.Author, "unknown"),
			Created: node.CreatedAt,
			TimeAgo: timeAgo(node.CreatedAt),
			Text:    template.HTML(node.Text),
			Depth:   depth,
		}

		if len(node.Children) > 0 {
			childIDs := make([]int, len(node.Children))
			for i, c := range node.Children {
				childIDs[i] = c.ID
			}
			cv.Children = buildAlgoliaCommentTree(childIDs, nodesByID, depth+1, storyID, sortMode)
		}

		out = append(out, cv)
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

func (s *server) filterSubmittedIDs(ctx context.Context, submitted []int, offset int, limit int, keep func(*hnItem) bool) ([]*hnItem, bool) {
	result := make([]*hnItem, 0, limit)
	pos := offset

	for len(result) < limit && pos < len(submitted) {
		end := pos + userSubmissionBatchSize
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

	skip := (page - 1) * userPageSize
	needed := skip + userPageSize + 1

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
