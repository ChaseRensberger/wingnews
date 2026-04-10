package main

import "time"

const (
	ttlFeedIDs     = 5 * time.Minute
	ttlItem        = 30 * time.Minute
	ttlUser        = 30 * time.Minute
	ttlCommentTree = 15 * time.Minute
	ttlGitHubStars = 24 * time.Hour
)

const (
	feedHydrateWorkers  = 8
	commentFetchWorkers = 24
	maxCommentNodes     = 50
	rootStoryMaxDepth   = 5
)

const (
	pageSize                = 30
	topLevelPageSize        = 20
	userPageSize            = 20
	userSubmissionBatchSize = 50
)

const (
	baseURL    = "https://news.wingman.actor"
	githubRepo = "ChaseRensberger/wingnews"
)
