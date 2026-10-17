// Package search: the user's own Tavily key as a second evidence source next to the search model.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Hit struct {
	Title, URL, Content, Date string
}

type Tavily struct {
	BaseURL string // default https://api.tavily.com
	Client  *http.Client
}

func (t Tavily) Search(ctx context.Context, key, query string, max int) ([]Hit, error) {
	base := t.BaseURL
	if base == "" {
		base = "https://api.tavily.com"
	}
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": max, "search_depth": "basic"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("tavily http %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(out.Results))
	for _, r := range out.Results {
		hits = append(hits, Hit{Title: r.Title, URL: r.URL, Content: r.Content, Date: r.PublishedDate})
	}
	return hits, nil
}
