package models

type SearchResponse struct {
	Query             string         `json:"query"`
	FollowUpQuestions any            `json:"follow_up_questions"`
	Answer            any            `json:"answer"`
	Images            []any          `json:"images"`
	Results           []SearchResult `json:"results"`
	ResponseTime      float64        `json:"response_time"`
	RequestId         string         `json:"request_id"`
}

type SearchResult struct {
	Url        string  `json:"url"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent any     `json:"raw_content"`
}
