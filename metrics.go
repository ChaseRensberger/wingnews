package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var metrics = &appMetrics{
	startTime: time.Now(),
}

type appMetrics struct {
	startTime time.Time

	hnFetchTotal  atomic.Int64
	hnFetchErrors atomic.Int64

	githubFetchTotal  atomic.Int64
	githubFetchErrors atomic.Int64

	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
}

type metricsSnapshot struct {
	UptimeSeconds float64 `json:"uptime_seconds"`

	HNFetchTotal  int64 `json:"hn_fetch_total"`
	HNFetchErrors int64 `json:"hn_fetch_errors"`

	GitHubFetchTotal  int64 `json:"github_fetch_total"`
	GitHubFetchErrors int64 `json:"github_fetch_errors"`

	CacheHits       int64   `json:"cache_hits"`
	CacheMisses     int64   `json:"cache_misses"`
	CacheHitRatePct float64 `json:"cache_hit_rate_pct"`
}

func (m *appMetrics) snapshot() metricsSnapshot {
	hits := m.cacheHits.Load()
	misses := m.cacheMisses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	return metricsSnapshot{
		UptimeSeconds:     time.Since(m.startTime).Seconds(),
		HNFetchTotal:      m.hnFetchTotal.Load(),
		HNFetchErrors:     m.hnFetchErrors.Load(),
		GitHubFetchTotal:  m.githubFetchTotal.Load(),
		GitHubFetchErrors: m.githubFetchErrors.Load(),
		CacheHits:         hits,
		CacheMisses:       misses,
		CacheHitRatePct:   hitRate,
	}
}

func handleDebugStats(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("DEBUG_TOKEN")
	if token == "" {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+token &&
		r.URL.Query().Get("token") != token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	snap := metrics.snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}
