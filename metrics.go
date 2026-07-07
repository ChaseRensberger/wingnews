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

	requestsTotal        atomic.Int64
	requestDurationNanos atomic.Int64
	responses2xx         atomic.Int64
	responses3xx         atomic.Int64
	responses4xx         atomic.Int64
	responses5xx         atomic.Int64

	hnFetchTotal  atomic.Int64
	hnFetchErrors atomic.Int64

	githubFetchTotal  atomic.Int64
	githubFetchErrors atomic.Int64

	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
}

type metricsSnapshot struct {
	UptimeSeconds float64 `json:"uptime_seconds"`

	RequestsTotal        int64   `json:"requests_total"`
	Responses2xx         int64   `json:"responses_2xx"`
	Responses3xx         int64   `json:"responses_3xx"`
	Responses4xx         int64   `json:"responses_4xx"`
	Responses5xx         int64   `json:"responses_5xx"`
	AvgRequestDurationMs float64 `json:"avg_request_duration_ms"`

	HNFetchTotal  int64 `json:"hn_fetch_total"`
	HNFetchErrors int64 `json:"hn_fetch_errors"`

	GitHubFetchTotal  int64 `json:"github_fetch_total"`
	GitHubFetchErrors int64 `json:"github_fetch_errors"`

	CacheHits       int64   `json:"cache_hits"`
	CacheMisses     int64   `json:"cache_misses"`
	CacheHitRatePct float64 `json:"cache_hit_rate_pct"`
}

func (m *appMetrics) recordRequest(status int, duration time.Duration) {
	m.requestsTotal.Add(1)
	m.requestDurationNanos.Add(duration.Nanoseconds())

	switch {
	case status >= 500:
		m.responses5xx.Add(1)
	case status >= 400:
		m.responses4xx.Add(1)
	case status >= 300:
		m.responses3xx.Add(1)
	case status >= 200:
		m.responses2xx.Add(1)
	}
}

func (m *appMetrics) snapshot() metricsSnapshot {
	hits := m.cacheHits.Load()
	misses := m.cacheMisses.Load()
	total := hits + misses
	requestsTotal := m.requestsTotal.Load()

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}
	var avgRequestDurationMs float64
	if requestsTotal > 0 {
		avgRequestDurationMs = float64(m.requestDurationNanos.Load()) / float64(requestsTotal) / float64(time.Millisecond)
	}

	return metricsSnapshot{
		UptimeSeconds:        time.Since(m.startTime).Seconds(),
		RequestsTotal:        requestsTotal,
		Responses2xx:         m.responses2xx.Load(),
		Responses3xx:         m.responses3xx.Load(),
		Responses4xx:         m.responses4xx.Load(),
		Responses5xx:         m.responses5xx.Load(),
		AvgRequestDurationMs: avgRequestDurationMs,
		HNFetchTotal:         m.hnFetchTotal.Load(),
		HNFetchErrors:        m.hnFetchErrors.Load(),
		GitHubFetchTotal:     m.githubFetchTotal.Load(),
		GitHubFetchErrors:    m.githubFetchErrors.Load(),
		CacheHits:            hits,
		CacheMisses:          misses,
		CacheHitRatePct:      hitRate,
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
