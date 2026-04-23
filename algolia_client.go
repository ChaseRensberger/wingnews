package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// algoliaItem mirrors the nested response from https://hn.algolia.com/api/v1/items/{id}.
// The full comment tree is returned in a single request, with children embedded recursively.
type algoliaItem struct {
	ID        int            `json:"id"`
	Type      string         `json:"type"` // "story", "comment", "job", "poll", "pollopt"
	Author    string         `json:"author"`
	CreatedAt int64          `json:"created_at_i"`
	Title     string         `json:"title"`
	Text      string         `json:"text"`
	URL       string         `json:"url"`
	Points    int            `json:"points"`
	StoryID   int            `json:"story_id"`
	ParentID  int            `json:"parent_id"`
	Children  []*algoliaItem `json:"children"`
}

type algoliaClient struct {
	http  *http.Client
	base  string
	cache *memoryCache
}

func newAlgoliaClient() *algoliaClient {
	return &algoliaClient{
		http:  &http.Client{Timeout: 8 * time.Second},
		base:  "https://hn.algolia.com/api/v1",
		cache: newAlgoliaMemoryCache(),
	}
}

func newAlgoliaMemoryCache() *memoryCache {
	return newMemoryCache(2000, 30*time.Second)
}

// getItemTree fetches the full nested item tree for the given story/item ID.
// Unlike the Firebase API which requires N requests for N nodes, this is a single HTTP call.
func (c *algoliaClient) getItemTree(ctx context.Context, id int) (*algoliaItem, error) {
	cacheKey := "algolia-tree:" + strconv.Itoa(id)
	value, err := c.cache.getOrLoad(ctx, cacheKey, ttlCommentTree, func(ctx context.Context) (any, error) {
		url := c.base + "/items/" + strconv.Itoa(id)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		metrics.hnFetchTotal.Add(1)
		resp, err := c.http.Do(req)
		if err != nil {
			metrics.hnFetchErrors.Add(1)
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			metrics.hnFetchErrors.Add(1)
			return nil, fmt.Errorf("algolia upstream status %d", resp.StatusCode)
		}

		var item algoliaItem
		if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
			return nil, err
		}
		return &item, nil
	})
	if err != nil {
		return nil, err
	}

	item, _ := value.(*algoliaItem)
	if item == nil {
		return nil, fmt.Errorf("algolia item %d not found", id)
	}
	return item, nil
}
