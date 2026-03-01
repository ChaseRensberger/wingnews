package main

import "html/template"

const pageSize = 30

type hnItem struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	By          string `json:"by"`
	Time        int64  `json:"time"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	URL         string `json:"url"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Kids        []int  `json:"kids"`
	Deleted     bool   `json:"deleted"`
	Dead        bool   `json:"dead"`
}

type hnUser struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	Karma     int    `json:"karma"`
	About     string `json:"about"`
	Submitted []int  `json:"submitted"`
}

type storyView struct {
	Rank         int
	ID           int
	Title        string
	URL          string
	DisplayURL   string
	Domain       string
	Score        int
	By           string
	TimeAgo      string
	CommentsText string
	Type         string
}

type commentView struct {
	ID              int
	By              string
	Created         int64
	TimeAgo         string
	Text            template.HTML
	Depth           int
	Deleted         bool
	Dead            bool
	Children        []*commentView
	HasMoreChildren bool
	HiddenChildren  int
	ExpandPath      string
}

type feedPageData struct {
	Feed    string
	Path    string
	Page    int
	HasMore bool
	Stories []storyView
	Error   string
}

type itemPageData struct {
	Story         storyView
	Text          template.HTML
	Comments      []*commentView
	Sort          string
	CommentsError string
	TotalComments int
}

type userPageData struct {
	ID         string
	Karma      int
	CreatedAgo string
	About      template.HTML
	Submitted  []storyView
	Error      string
}

type layoutData struct {
	Title   string
	Active  string
	Content template.HTML
}

type server struct {
	hn            *hnClient
	tmpl          *template.Template
	commentsCache *memoryCache
}
