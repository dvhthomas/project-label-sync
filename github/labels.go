package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// LabelManager handles label operations via the GitHub REST API.
type LabelManager struct {
	HTTPClient *http.Client
	Token      string
	DryRun     bool
}

// NewLabelManager creates a LabelManager using the given HTTP client and token.
func NewLabelManager(httpClient *http.Client, token string, dryRun bool) *LabelManager {
	return &LabelManager{HTTPClient: httpClient, Token: token, DryRun: dryRun}
}

// EnsureLabelExists creates the label if it does not already exist.
// Uses POST /repos/{owner}/{repo}/labels. A 422 "already_exists" response
// is treated as success.
func (m *LabelManager) EnsureLabelExists(ctx context.Context, repo, labelName string) error {
	return withRetry(ctx, "ensure-label-"+labelName, 3, func() error {
		if m.DryRun {
			log.Printf("[dry-run] Would ensure label %q exists on %s", labelName, repo)
			return nil
		}

		body, err := json.Marshal(map[string]string{
			"name":        labelName,
			"color":       "ededed",
			"description": "Synced from project board status",
		})
		if err != nil {
			return fmt.Errorf("marshal label body: %w", err)
		}

		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/labels", repo)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+m.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := m.HTTPClient.Do(req)
		if err != nil {
			return &retryableError{err: err}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return &retryableError{err: fmt.Errorf("read response: %w", err)}
		}

		// 201 Created = success, 422 with "already_exists" = also success.
		if resp.StatusCode == http.StatusCreated {
			log.Printf("::notice::Created label %q on %s", labelName, repo)
			return nil
		}

		if resp.StatusCode == http.StatusUnprocessableEntity {
			if strings.Contains(string(respBody), "already_exists") {
				return nil
			}
		}

		if isRetryableStatus(resp.StatusCode) {
			return &retryableError{err: fmt.Errorf("ensure label %q: HTTP %d: %s", labelName, resp.StatusCode, string(respBody))}
		}

		return fmt.Errorf("ensure label %q on %s: HTTP %d: %s", labelName, repo, resp.StatusCode, string(respBody))
	})
}

// AddLabel adds a label to an issue via POST /repos/{owner}/{repo}/issues/{number}/labels.
func (m *LabelManager) AddLabel(ctx context.Context, repo string, issueNumber int, labelName string) error {
	return withRetry(ctx, "add-label-"+labelName, 3, func() error {
		if m.DryRun {
			log.Printf("[dry-run] Would add label %q to %s#%d", labelName, repo, issueNumber)
			return nil
		}

		body, err := json.Marshal(map[string][]string{
			"labels": {labelName},
		})
		if err != nil {
			return fmt.Errorf("marshal label body: %w", err)
		}

		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/issues/%s/labels",
			repo, strconv.Itoa(issueNumber))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+m.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := m.HTTPClient.Do(req)
		if err != nil {
			return &retryableError{err: err}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return &retryableError{err: fmt.Errorf("read response: %w", err)}
		}

		if resp.StatusCode == http.StatusOK {
			log.Printf("::notice::Added label %q to %s#%d", labelName, repo, issueNumber)
			return nil
		}

		if isRetryableStatus(resp.StatusCode) {
			return &retryableError{err: fmt.Errorf("add label: HTTP %d: %s", resp.StatusCode, string(respBody))}
		}

		return fmt.Errorf("add label %q to %s#%d: HTTP %d: %s", labelName, repo, issueNumber, resp.StatusCode, string(respBody))
	})
}

// RemoveLabel removes a label from an issue via DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}.
// A 404 response is treated as success (label already removed).
func (m *LabelManager) RemoveLabel(ctx context.Context, repo string, issueNumber int, labelName string) error {
	return withRetry(ctx, "remove-label-"+labelName, 3, func() error {
		if m.DryRun {
			log.Printf("[dry-run] Would remove label %q from %s#%d", labelName, repo, issueNumber)
			return nil
		}

		encodedLabel := url.PathEscape(labelName)
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/issues/%s/labels/%s",
			repo, strconv.Itoa(issueNumber), encodedLabel)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+m.Token)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := m.HTTPClient.Do(req)
		if err != nil {
			return &retryableError{err: err}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return &retryableError{err: fmt.Errorf("read response: %w", err)}
		}

		// 200 OK or 204 No Content = success, 404 = already removed.
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
			if resp.StatusCode != http.StatusNotFound {
				log.Printf("::notice::Removed label %q from %s#%d", labelName, repo, issueNumber)
			}
			return nil
		}

		if isRetryableStatus(resp.StatusCode) {
			return &retryableError{err: fmt.Errorf("remove label: HTTP %d: %s", resp.StatusCode, string(respBody))}
		}

		return fmt.Errorf("remove label %q from %s#%d: HTTP %d: %s", labelName, repo, issueNumber, resp.StatusCode, string(respBody))
	})
}

// CheckLabelsExist checks which of the given labels already exist on the
// repository and which are missing. Uses GET /repos/{owner}/{repo}/labels
// with pagination.
func (m *LabelManager) CheckLabelsExist(ctx context.Context, repo string, labels []string) (existing, missing []string, err error) {
	repoLabels := make(map[string]bool)
	page := 1
	for {
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/labels?per_page=100&page=%d", repo, page)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if reqErr != nil {
			return nil, nil, fmt.Errorf("create request: %w", reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+m.Token)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, doErr := m.HTTPClient.Do(req)
		if doErr != nil {
			return nil, nil, fmt.Errorf("list labels: %w", doErr)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, nil, fmt.Errorf("read response: %w", readErr)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("list labels on %s: HTTP %d: %s", repo, resp.StatusCode, string(body))
		}

		var pageLabels []struct {
			Name string `json:"name"`
		}
		if jsonErr := json.Unmarshal(body, &pageLabels); jsonErr != nil {
			return nil, nil, fmt.Errorf("unmarshal labels: %w", jsonErr)
		}

		for _, l := range pageLabels {
			repoLabels[l.Name] = true
		}

		// If fewer than 100 results, we've reached the last page.
		if len(pageLabels) < 100 {
			break
		}
		page++
	}

	for _, l := range labels {
		if repoLabels[l] {
			existing = append(existing, l)
		} else {
			missing = append(missing, l)
		}
	}
	return existing, missing, nil
}

// isRetryableStatus returns true for HTTP status codes that indicate
// transient failures worth retrying.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusForbidden ||
		code == http.StatusInternalServerError ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}
