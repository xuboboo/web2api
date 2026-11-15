package core

import "time"

// QueryEcho echoes the interpreted query parameters back to the client.
type QueryEcho struct {
	Text             string   `json:"text"`
	Lang             string   `json:"lang,omitempty"`
	Region           string   `json:"region,omitempty"`
	EnginesRequested []string `json:"engines_requested"`
}

// ResponseMeta carries request-level metadata for observability and debugging.
type ResponseMeta struct {
	RequestID        string   `json:"request_id"`
	RequestedAt      string   `json:"requested_at"`
	TookMs           int64    `json:"took_ms"`
	EnginesResponded []string `json:"engines_responded,omitempty"`
	EnginesFailed    []string `json:"engines_failed"`
	Version          string   `json:"version"`
	// EnginesCooldown lists the engines currently in an IP cooldown (契约 A):
	// [{"engine":"baidu","until":"...","reason":"COOLDOWN"}]. Omitted when no
	// engine is cooling.
	EnginesCooldown []EngineCooldownEntry `json:"engines_cooldown,omitempty"`
	// Partial and TotalResults are set only by engines that report them.
	Partial      bool  `json:"partial,omitempty"`
	TotalResults int64 `json:"total_results,omitempty"`
}

// Pagination carries cursor information for client-side loop termination.
type Pagination struct {
	Page      int  `json:"page"`
	HasMore   bool `json:"has_more"`
	NextStart int  `json:"next_start"`
}

// Envelope is the top-level v2 response wrapper for all search endpoints.
type Envelope struct {
	Query        QueryEcho     `json:"query"`
	Meta         ResponseMeta  `json:"meta"`
	Results      []Result      `json:"results"`
	SerpFeatures []SerpFeature `json:"serp_features"`
	Pagination   Pagination    `json:"pagination"`
}

const apiVersion = "2.1"

// NewEnvelope builds a fresh Envelope pre-filled with query echo and an open
// meta block. Call Finalize before serializing.
func NewEnvelope(q Query, requestID string, startedAt time.Time, engines []string) *Envelope {
	return &Envelope{
		Query: QueryEcho{
			Text:             q.Text,
			Lang:             q.LangCode,
			Region:           q.Region,
			EnginesRequested: engines,
		},
		Meta: ResponseMeta{
			RequestID:     requestID,
			RequestedAt:   startedAt.UTC().Format(time.RFC3339),
			EnginesFailed: []string{},
			Version:       apiVersion,
		},
		Results:      []Result{},
		SerpFeatures: []SerpFeature{},
		Pagination:   Pagination{},
	}
}

// Finalize stamps the elapsed time and computes pagination fields.
func (e *Envelope) Finalize(startedAt time.Time, q Query) {
	e.Meta.TookMs = time.Since(startedAt).Milliseconds()

	limit := q.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	page := q.Start/limit + 1
	e.Pagination = Pagination{
		Page:      page,
		HasMore:   countNonAdResults(e.Results) >= limit,
		NextStart: q.Start + limit,
	}
}

func countNonAdResults(results []Result) int {
	count := 0
	for _, result := range results {
		if result.Type != ResultTypeAd {
			count++
		}
	}
	return count
}
