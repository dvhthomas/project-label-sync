package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	applog "github.com/dvhthomas/project-label-sync/internal/log"
)

// SearchResult represents a single issue returned by the GitHub Search API.
type SearchResult struct {
	Number    int
	Title     string
	State     string
	Labels    []string // label names
	RepoOwner string
	RepoName  string
	HTMLURL   string
}

// searchResponse is the JSON envelope from GET /search/issues.
type searchResponse struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             []struct {
		Number        int    `json:"number"`
		Title         string `json:"title"`
		State         string `json:"state"`
		HTMLURL       string `json:"html_url"`
		RepositoryURL string `json:"repository_url"`
		Labels        []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"items"`
}

// SearchOpenIssuesInProject returns all open issues in a GitHub Projects v2
// board using the Search API. This is much more efficient than paginating
// through the Projects items connection for boards with many closed items.
//
// Uses: GET /search/issues?q=is:open+is:issue+project:{owner}/{number}
func (c *Client) SearchOpenIssuesInProject(ctx context.Context, projectOwner string, projectNumber int) ([]SearchResult, error) {
	query := fmt.Sprintf("is:open is:issue project:%s/%d", projectOwner, projectNumber)

	var allResults []SearchResult
	page := 1

	for {
		// Throttle between search API pages (30 req/min limit).
		if page > 1 {
			applog.Notice("Search throttle: waiting 2s before page %d", page)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}

		apiURL := fmt.Sprintf(
			"https://api.github.com/search/issues?q=%s&per_page=100&page=%d",
			strings.ReplaceAll(query, " ", "+"),
			page,
		)

		var resp searchResponse
		err := withRetry(ctx, fmt.Sprintf("search-page-%d", page), 3, func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("Authorization", "Bearer "+c.Token)
			req.Header.Set("Accept", "application/vnd.github+json")

			httpResp, err := c.HTTPClient.Do(req)
			if err != nil {
				return &retryableError{err: err}
			}
			defer httpResp.Body.Close()

			c.trackRateLimit(httpResp.Header)

			body, err := io.ReadAll(httpResp.Body)
			if err != nil {
				return &retryableError{err: fmt.Errorf("read response: %w", err)}
			}

			if httpResp.StatusCode == http.StatusForbidden || httpResp.StatusCode == 429 {
				retryAfter := parseRetryAfter(httpResp.Header)
				return &retryableError{
					err:        fmt.Errorf("search rate limited (HTTP %d): %s", httpResp.StatusCode, string(body)),
					retryAfter: retryAfter,
				}
			}

			if httpResp.StatusCode >= 500 {
				return &retryableError{err: fmt.Errorf("server error (HTTP %d): %s", httpResp.StatusCode, string(body))}
			}

			if httpResp.StatusCode != http.StatusOK {
				return fmt.Errorf("search HTTP %d: %s", httpResp.StatusCode, string(body))
			}

			if err := json.Unmarshal(body, &resp); err != nil {
				return fmt.Errorf("unmarshal search response: %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("search open issues (page %d): %w", page, err)
		}

		for _, item := range resp.Items {
			owner, repo := parseRepositoryURL(item.RepositoryURL)
			if owner == "" || repo == "" {
				continue
			}

			var labels []string
			for _, l := range item.Labels {
				labels = append(labels, l.Name)
			}

			allResults = append(allResults, SearchResult{
				Number:    item.Number,
				Title:     item.Title,
				State:     item.State,
				Labels:    labels,
				RepoOwner: owner,
				RepoName:  repo,
				HTMLURL:   item.HTMLURL,
			})
		}

		// The search API returns at most 1000 results.
		if len(resp.Items) < 100 || len(allResults) >= resp.TotalCount {
			break
		}
		page++
	}

	applog.Notice("Search found %d open issues across %d pages", len(allResults), page)
	return allResults, nil
}

// parseRepositoryURL extracts owner and repo from a repository_url like
// "https://api.github.com/repos/owner/repo".
func parseRepositoryURL(repoURL string) (owner, repo string) {
	// Expected format: https://api.github.com/repos/{owner}/{repo}
	const prefix = "https://api.github.com/repos/"
	if !strings.HasPrefix(repoURL, prefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(repoURL, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
