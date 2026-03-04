package main

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func parsePositiveInt(raw string, fallbackValue int) int {
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallbackValue
	}
	return parsed
}

func parseSort(raw string) string {
	if raw == "new" {
		return "new"
	}
	return "hn"
}

func isHXRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

func timeAgo(unix int64) string {
	if unix <= 0 {
		return "unknown"
	}
	d := time.Since(time.Unix(unix, 0))
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		m := int(d.Minutes())
		return fmt.Sprintf("%d minute%s ago", m, plural(m))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		return fmt.Sprintf("%d hour%s ago", h, plural(h))
	}
	if d < 30*24*time.Hour {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%d day%s ago", days, plural(days))
	}
	months := int(d.Hours() / 24 / 30)
	if months < 12 {
		return fmt.Sprintf("%d month%s ago", months, plural(months))
	}
	years := months / 12
	return fmt.Sprintf("%d year%s ago", years, plural(years))
}

func plural(v int) string {
	if v == 1 {
		return ""
	}
	return "s"
}

func domainFromURL(raw string) string {
	if raw == "" {
		return "news.ycombinator.com"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.TrimPrefix(host, "www.")
}

func fallback(value, fallbackValue string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallbackValue
	}
	return trimmed
}

func toStoryView(item *hnItem, rank int) storyView {
	targetURL := item.URL
	if targetURL == "" {
		targetURL = "/item/" + strconv.Itoa(item.ID)
	}

	commentsText := "discuss"
	if item.Type == "job" {
		commentsText = "job"
	} else if item.Descendants == 1 {
		commentsText = "1 comment"
	} else if item.Descendants > 0 {
		commentsText = fmt.Sprintf("%d comments", item.Descendants)
	}

	return storyView{
		Rank:         rank,
		ID:           item.ID,
		Title:        fallback(item.Title, "(no title)"),
		URL:          targetURL,
		DisplayURL:   item.URL,
		Domain:       domainFromURL(item.URL),
		Score:        item.Score,
		By:           fallback(item.By, "unknown"),
		TimeAgo:      timeAgo(item.Time),
		CommentsText: commentsText,
		Type:         item.Type,
	}
}

func sortCommentsByNewest(nodes []*commentView) {
	for _, node := range nodes {
		sortCommentsByNewest(node.Children)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].Created > nodes[j].Created
	})
}

// paginateIDs returns a slice of ids starting at offset with at most limit items,
// and the count of remaining ids after this page.
func paginateIDs(ids []int, offset, limit int) (page []int, remaining int) {
	if offset >= len(ids) {
		return nil, 0
	}
	end := offset + limit
	if end >= len(ids) {
		return ids[offset:], 0
	}
	return ids[offset:end], len(ids) - end
}
