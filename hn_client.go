package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type hnClient struct {
	http  *http.Client
	base  string
	cache *memoryCache
}

func newHNClient() *hnClient {
	return &hnClient{
		http:  &http.Client{Timeout: 10 * time.Second},
		base:  "https://hn.algolia.com/api/v1",
		cache: newHNMemoryCache(),
	}
}

// Algolia API response types

type algoliaSearchResult struct {
	Hits    []algoliaHit `json:"hits"`
	NbHits  int          `json:"nbHits"`
	Page    int          `json:"page"`
	NbPages int          `json:"nbPages"`
}

type algoliaHit struct {
	ObjectID    string   `json:"objectID"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Points      int      `json:"points"`
	Author      string   `json:"author"`
	CreatedAtI  int64    `json:"created_at_i"`
	NumComments int      `json:"num_comments"`
	StoryID     *int     `json:"story_id"`
	StoryTitle  string   `json:"story_title"`
	ParentID    *int     `json:"parent_id"`
	StoryText   string   `json:"story_text"`
	CommentText string   `json:"comment_text"`
	Tags        []string `json:"_tags"`
}

type algoliaItem struct {
	ID         int            `json:"id"`
	CreatedAtI int64          `json:"created_at_i"`
	Type       string         `json:"type"`
	Author     string         `json:"author"`
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	Text       string         `json:"text"`
	Points     *int           `json:"points"`
	ParentID   *int           `json:"parent_id"`
	StoryID    *int           `json:"story_id"`
	Children   []*algoliaItem `json:"children"`
}

type algoliaUser struct {
	ID         string `json:"id"`
	About      string `json:"about"`
	Karma      int    `json:"karma"`
	CreatedAtI int64  `json:"created_at_i"`
}

func (c *hnClient) fetchJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}

	metrics.hnFetchTotal.Add(1)
	resp, err := c.http.Do(req)
	if err != nil {
		metrics.hnFetchErrors.Add(1)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.hnFetchErrors.Add(1)
		return fmt.Errorf("upstream status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// getFeedPage returns one page of stories for the named feed.
// page is 1-indexed. Returns the items and whether more pages exist.
// This replaces the old getFeedIDs + hydrateStories two-step pattern:
// instead of fetching 500 IDs then N individual item requests, a single
// Algolia search request returns fully-hydrated stories for the page.
func (c *hnClient) getFeedPage(ctx context.Context, feed string, page int) ([]*hnItem, bool, error) {
	if page < 1 {
		page = 1
	}
	algoPage := page - 1 // Algolia uses 0-indexed pages

	type feedConfig struct {
		endpoint string
		tags     string
	}
	feedCfg := map[string]feedConfig{
		"top":  {"/search", "front_page"},
		"new":  {"/search_by_date", "story"},
		"best": {"/search", "story"},
		"ask":  {"/search", "ask_hn"},
		"show": {"/search", "show_hn"},
		"jobs": {"/search", "job"},
	}

	cfg, ok := feedCfg[feed]
	if !ok {
		return nil, false, fmt.Errorf("unknown feed %q", feed)
	}

	cacheKey := fmt.Sprintf("feed-page:%s:%d", feed, page)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlFeedIDs, func(ctx context.Context) (any, error) {
		params := url.Values{}
		params.Set("tags", cfg.tags)
		params.Set("hitsPerPage", strconv.Itoa(pageSize))
		params.Set("page", strconv.Itoa(algoPage))

		var result algoliaSearchResult
		if err := c.fetchJSON(ctx, cfg.endpoint+"?"+params.Encode(), &result); err != nil {
			return nil, err
		}

		items := make([]*hnItem, 0, len(result.Hits))
		for i := range result.Hits {
			items = append(items, algoliaHitToHNItem(&result.Hits[i]))
		}
		hasMore := result.Page+1 < result.NbPages
		return struct {
			Items   []*hnItem
			HasMore bool
		}{items, hasMore}, nil
	})
	if err != nil {
		return nil, false, err
	}

	payload, _ := value.(struct {
		Items   []*hnItem
		HasMore bool
	})
	return payload.Items, payload.HasMore, nil
}

// getItem fetches a single item by ID using Algolia's /items/{id} endpoint.
func (c *hnClient) getItem(ctx context.Context, id int) (*hnItem, error) {
	cacheKey := "item:" + strconv.Itoa(id)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlItem, func(ctx context.Context) (any, error) {
		var a algoliaItem
		if err := c.fetchJSON(ctx, fmt.Sprintf("/items/%d", id), &a); err != nil {
			return nil, err
		}
		if a.ID == 0 {
			return nil, fmt.Errorf("item %d not found", id)
		}
		return algoliaItemToHNItem(&a), nil
	})
	if err != nil {
		return nil, err
	}

	item, _ := value.(*hnItem)
	if item == nil {
		return nil, fmt.Errorf("item %d not found", id)
	}
	return item, nil
}

// getItemTree fetches a story with its full nested comment tree in a single
// Algolia request, then pre-warms the item cache with every descendant so
// that subsequent comment-expansion requests are served entirely from cache.
func (c *hnClient) getItemTree(ctx context.Context, id int) (*hnItem, error) {
	cacheKey := "item:" + strconv.Itoa(id)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlItem, func(ctx context.Context) (any, error) {
		var a algoliaItem
		if err := c.fetchJSON(ctx, fmt.Sprintf("/items/%d", id), &a); err != nil {
			return nil, err
		}
		if a.ID == 0 {
			return nil, fmt.Errorf("item %d not found", id)
		}
		// Pre-warm descendants so comment fetches hit cache
		for _, child := range a.Children {
			c.prewarmTree(child)
		}
		item := algoliaItemToHNItem(&a)
		item.Descendants = countDescendants(&a)
		return item, nil
	})
	if err != nil {
		return nil, err
	}

	item, _ := value.(*hnItem)
	if item == nil {
		return nil, fmt.Errorf("item %d not found", id)
	}
	return item, nil
}

// prewarmTree recursively stores each algoliaItem in the item cache
// so subsequent getItem calls are served without a network request.
func (c *hnClient) prewarmTree(a *algoliaItem) {
	if a == nil || a.ID == 0 {
		return
	}
	key := "item:" + strconv.Itoa(a.ID)
	exp := time.Now().Add(ttlItem)
	c.cache.mu.Lock()
	if _, exists := c.cache.entries[key]; !exists {
		c.cache.upsert(key, algoliaItemToHNItem(a), exp)
	}
	c.cache.mu.Unlock()
	for _, child := range a.Children {
		c.prewarmTree(child)
	}
}

// countDescendants returns the total number of non-nil descendants in the tree.
func countDescendants(a *algoliaItem) int {
	n := 0
	for _, child := range a.Children {
		if child != nil {
			n += 1 + countDescendants(child)
		}
	}
	return n
}

// getUser fetches a user profile from Algolia's /users/{id} endpoint.
func (c *hnClient) getUser(ctx context.Context, id string) (*hnUser, error) {
	cacheKey := "user:" + strings.ToLower(id)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlUser, func(ctx context.Context) (any, error) {
		var u algoliaUser
		if err := c.fetchJSON(ctx, "/users/"+url.PathEscape(id), &u); err != nil {
			return nil, err
		}
		if u.ID == "" {
			return nil, fmt.Errorf("user %q not found", id)
		}
		return algoliaUserToHNUser(&u), nil
	})
	if err != nil {
		return nil, err
	}

	user, _ := value.(*hnUser)
	if user == nil {
		return nil, fmt.Errorf("user %q not found", id)
	}
	return user, nil
}

// getUserStories returns a page of stories submitted by a user.
// This replaces the old getUser + filterSubmittedIDs pattern with a
// single Algolia search that is already paginated.
func (c *hnClient) getUserStories(ctx context.Context, username string, page int) ([]*hnItem, bool, error) {
	if page < 1 {
		page = 1
	}
	cacheKey := fmt.Sprintf("user-stories:%s:%d", strings.ToLower(username), page)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlUser, func(ctx context.Context) (any, error) {
		params := url.Values{}
		params.Set("tags", "story,author_"+username)
		params.Set("hitsPerPage", strconv.Itoa(userPageSize))
		params.Set("page", strconv.Itoa(page-1))

		var result algoliaSearchResult
		if err := c.fetchJSON(ctx, "/search_by_date?"+params.Encode(), &result); err != nil {
			return nil, err
		}

		items := make([]*hnItem, 0, len(result.Hits))
		for i := range result.Hits {
			items = append(items, algoliaHitToHNItem(&result.Hits[i]))
		}
		hasMore := result.Page+1 < result.NbPages
		return struct {
			Items   []*hnItem
			HasMore bool
		}{items, hasMore}, nil
	})
	if err != nil {
		return nil, false, err
	}

	payload, _ := value.(struct {
		Items   []*hnItem
		HasMore bool
	})
	return payload.Items, payload.HasMore, nil
}

// getUserComments returns a page of comments posted by a user.
// Items have StoryID and StoryTitle populated so the caller does not need
// to call getRootStory for each comment.
func (c *hnClient) getUserComments(ctx context.Context, username string, page int) ([]*hnItem, bool, error) {
	if page < 1 {
		page = 1
	}
	cacheKey := fmt.Sprintf("user-comments:%s:%d", strings.ToLower(username), page)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlUser, func(ctx context.Context) (any, error) {
		params := url.Values{}
		params.Set("tags", "comment,author_"+username)
		params.Set("hitsPerPage", strconv.Itoa(userPageSize))
		params.Set("page", strconv.Itoa(page-1))

		var result algoliaSearchResult
		if err := c.fetchJSON(ctx, "/search_by_date?"+params.Encode(), &result); err != nil {
			return nil, err
		}

		items := make([]*hnItem, 0, len(result.Hits))
		for i := range result.Hits {
			items = append(items, algoliaHitToHNItem(&result.Hits[i]))
		}
		hasMore := result.Page+1 < result.NbPages
		return struct {
			Items   []*hnItem
			HasMore bool
		}{items, hasMore}, nil
	})
	if err != nil {
		return nil, false, err
	}

	payload, _ := value.(struct {
		Items   []*hnItem
		HasMore bool
	})
	return payload.Items, payload.HasMore, nil
}

// getRootStory returns the root story for a comment by traversing the parent
// chain. Items loaded via getItemTree are already in cache, so this is
// typically free. For fresh comment items from user pages the traversal
// makes at most rootStoryMaxDepth API calls.
func (c *hnClient) getRootStory(ctx context.Context, item *hnItem) *hnItem {
	current := item
	for range rootStoryMaxDepth {
		if current.Type != "comment" || current.Parent == 0 {
			break
		}
		parent, err := c.getItem(ctx, current.Parent)
		if err != nil {
			return nil
		}
		current = parent
	}
	if current.Type == "comment" {
		return nil
	}
	return current
}

// Conversion helpers

func algoliaHitToHNItem(h *algoliaHit) *hnItem {
	id, _ := strconv.Atoi(h.ObjectID)
	itemType := "story"
	for _, tag := range h.Tags {
		switch tag {
		case "comment":
			itemType = "comment"
		case "job":
			itemType = "job"
		}
	}
	text := h.StoryText
	if itemType == "comment" {
		text = h.CommentText
	}
	parent := 0
	if h.ParentID != nil {
		parent = *h.ParentID
	}
	storyID := 0
	if h.StoryID != nil {
		storyID = *h.StoryID
	}
	return &hnItem{
		ID:          id,
		Type:        itemType,
		By:          h.Author,
		Time:        h.CreatedAtI,
		Title:       h.Title,
		Text:        text,
		URL:         h.URL,
		Score:       h.Points,
		Parent:      parent,
		Descendants: h.NumComments,
		StoryID:     storyID,
		StoryTitle:  h.StoryTitle,
	}
}

func algoliaItemToHNItem(a *algoliaItem) *hnItem {
	parent := 0
	if a.ParentID != nil {
		parent = *a.ParentID
	}
	points := 0
	if a.Points != nil {
		points = *a.Points
	}
	kids := make([]int, 0, len(a.Children))
	for _, child := range a.Children {
		if child != nil {
			kids = append(kids, child.ID)
		}
	}
	return &hnItem{
		ID:     a.ID,
		Type:   a.Type,
		By:     a.Author,
		Time:   a.CreatedAtI,
		Title:  a.Title,
		Text:   a.Text,
		URL:    a.URL,
		Score:  points,
		Parent: parent,
		Kids:   kids,
	}
}

func algoliaUserToHNUser(u *algoliaUser) *hnUser {
	return &hnUser{
		ID:      u.ID,
		Created: u.CreatedAtI,
		Karma:   u.Karma,
		About:   u.About,
	}
}
