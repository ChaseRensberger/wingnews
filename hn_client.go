package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type hnClient struct {
	http  *http.Client
	base  string
	cache *memoryCache
}

func newHNClient() *hnClient {
	return &hnClient{
		http:  &http.Client{Timeout: 6 * time.Second},
		base:  "https://hacker-news.firebaseio.com/v0",
		cache: newHNMemoryCache(),
	}
}

func (c *hnClient) fetchJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *hnClient) getFeedIDs(ctx context.Context, feed string) ([]int, error) {
	endpointByFeed := map[string]string{
		"top":  "topstories",
		"new":  "newstories",
		"best": "beststories",
		"ask":  "askstories",
		"show": "showstories",
		"jobs": "jobstories",
	}

	endpoint, ok := endpointByFeed[feed]
	if !ok {
		return nil, fmt.Errorf("unknown feed %q", feed)
	}

	value, err := c.cache.getOrLoad(ctx, "feed:"+feed, 45*time.Second, func(ctx context.Context) (any, error) {
		var ids []int
		if err := c.fetchJSON(ctx, "/"+endpoint+".json", &ids); err != nil {
			return nil, err
		}
		return ids, nil
	})
	if err != nil {
		return nil, err
	}

	ids, _ := value.([]int)
	return ids, nil
}

func (c *hnClient) getItem(ctx context.Context, id int) (*hnItem, error) {
	cacheKey := "item:" + strconv.Itoa(id)
	value, err := c.cache.getOrLoad(ctx, cacheKey, 3*time.Minute, func(ctx context.Context) (any, error) {
		var item *hnItem
		if err := c.fetchJSON(ctx, "/item/"+strconv.Itoa(id)+".json", &item); err != nil {
			return nil, err
		}
		if item == nil {
			return nil, fmt.Errorf("item %d not found", id)
		}
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

func (c *hnClient) getUser(ctx context.Context, id string) (*hnUser, error) {
	cacheKey := "user:" + strings.ToLower(id)
	value, err := c.cache.getOrLoad(ctx, cacheKey, 10*time.Minute, func(ctx context.Context) (any, error) {
		var user *hnUser
		if err := c.fetchJSON(ctx, "/user/"+url.PathEscape(id)+".json", &user); err != nil {
			return nil, err
		}
		if user == nil {
			return nil, fmt.Errorf("user %q not found", id)
		}
		return user, nil
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

func (c *hnClient) hydrateStories(ctx context.Context, ids []int) []*hnItem {
	if len(ids) == 0 {
		return nil
	}

	workers := 8
	if len(ids) < workers {
		workers = len(ids)
	}

	out := make([]*hnItem, len(ids))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				item, err := c.getItem(ctx, ids[i])
				if err != nil {
					continue
				}
				out[i] = item
			}
		}()
	}

	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return out
}
