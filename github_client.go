package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const githubRepo = "ChaseRensberger/wingnews"

type githubClient struct {
	http  *http.Client
	cache *memoryCache
	repo  string
}

func newGitHubClient(repo string) *githubClient {
	return &githubClient{
		http:  &http.Client{Timeout: 4 * time.Second},
		cache: newMemoryCache(32, 30*time.Second),
		repo:  repo,
	}
}

func (c *githubClient) getRepoStars(ctx context.Context) (int, error) {
	value, err := c.cache.getOrLoad(ctx, "github:stars:"+c.repo, 10*time.Minute, func(ctx context.Context) (any, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+c.repo, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "wingnews")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github api status %d", resp.StatusCode)
		}

		var payload struct {
			StargazersCount int `json:"stargazers_count"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, err
		}

		return payload.StargazersCount, nil
	})
	if err != nil {
		return 0, err
	}

	stars, _ := value.(int)
	return stars, nil
}

func formatStarCount(stars int) string {
	if stars < 1000 {
		return strconv.Itoa(stars)
	}
	if stars < 1_000_000 {
		v := float64(stars) / 1000
		s := strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", v), "0"), ".")
		return s + "K"
	}

	v := float64(stars) / 1_000_000
	s := strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", v), "0"), ".")
	return s + "M"
}
