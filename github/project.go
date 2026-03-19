package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// ResolveProject looks up a Projects v2 board by URL and returns its
// metadata including the Status field options.
//
// Supported URL formats:
//
//	https://github.com/users/<login>/projects/<number>
//	https://github.com/orgs/<login>/projects/<number>
func (c *Client) ResolveProject(ctx context.Context, projectURL string) (*ProjectInfo, error) {
	ownerType, login, number, err := parseProjectURL(projectURL)
	if err != nil {
		return nil, err
	}

	// Build query based on owner type (user vs org).
	var query string
	switch ownerType {
	case "users":
		query = `query($login: String!, $number: Int!) {
  user(login: $login) {
    projectV2(number: $number) {
      id
      title
      field(name: "Status") {
        ... on ProjectV2SingleSelectField {
          id
          options { id name }
        }
      }
    }
  }
}`
	case "orgs":
		query = `query($login: String!, $number: Int!) {
  organization(login: $login) {
    projectV2(number: $number) {
      id
      title
      field(name: "Status") {
        ... on ProjectV2SingleSelectField {
          id
          options { id name }
        }
      }
    }
  }
}`
	}

	vars := map[string]any{
		"login":  login,
		"number": number,
	}

	var data map[string]json.RawMessage
	if err := c.GraphQL(ctx, "resolve-project", query, vars, &data); err != nil {
		return nil, fmt.Errorf("resolve project: %w", err)
	}

	// The project payload is nested under either "user" or "organization".
	var ownerKey string
	switch ownerType {
	case "users":
		ownerKey = "user"
	case "orgs":
		ownerKey = "organization"
	}

	var ownerData struct {
		ProjectV2 struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Field struct {
				ID      string `json:"id"`
				Options []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"options"`
			} `json:"field"`
		} `json:"projectV2"`
	}

	raw, ok := data[ownerKey]
	if !ok {
		return nil, fmt.Errorf("unexpected response: missing %q key", ownerKey)
	}
	if err := json.Unmarshal(raw, &ownerData); err != nil {
		return nil, fmt.Errorf("unmarshal owner data: %w", err)
	}

	p := ownerData.ProjectV2
	if p.ID == "" {
		return nil, fmt.Errorf("project not found at %s", projectURL)
	}
	if p.Field.ID == "" {
		return nil, fmt.Errorf("project %q has no Status single-select field", p.Title)
	}

	info := &ProjectInfo{
		ID:      p.ID,
		Title:   p.Title,
		FieldID: p.Field.ID,
	}
	for _, opt := range p.Field.Options {
		info.Options = append(info.Options, StatusOption{ID: opt.ID, Name: opt.Name})
	}

	log.Printf("::notice::Resolved project %q (ID: %s) with %d status options", info.Title, info.ID, len(info.Options))
	return info, nil
}

// ItemStatus holds the project board status and label timeline for a single issue.
type ItemStatus struct {
	ItemID      string
	BoardStatus string
	UpdatedAt   time.Time
	LabelEvents map[string]time.Time // label name -> most recent event time
}

// batchSize is the number of issues fetched per GraphQL call.
const batchSize = 20

// BatchFetchItemStatus fetches project item status and label timeline events
// for a batch of issues using aliased GraphQL queries. It filters projectItems
// to the target project ID.
func (c *Client) BatchFetchItemStatus(ctx context.Context, projectID string, issueNumbers []int, repoOwner, repoName string) (map[int]ItemStatus, error) {
	if len(issueNumbers) == 0 {
		return nil, nil
	}

	// Build aliased query fragments and variables.
	var fragments []string
	vars := map[string]any{
		"owner": repoOwner,
		"repo":  repoName,
	}
	var varDecls []string
	varDecls = append(varDecls, "$owner: String!", "$repo: String!")

	for i, num := range issueNumbers {
		alias := fmt.Sprintf("i%d", i)
		varName := fmt.Sprintf("n%d", i)
		varDecls = append(varDecls, fmt.Sprintf("$%s: Int!", varName))
		vars[varName] = num

		fragments = append(fragments, fmt.Sprintf(`    %s: issue(number: $%s) {
      number
      projectItems(first: 10) {
        nodes {
          id
          updatedAt
          project { id }
          fieldValues(first: 20) {
            nodes {
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                updatedAt
                field { ... on ProjectV2SingleSelectField { name } }
              }
            }
          }
        }
      }
      timelineItems(last: 50, itemTypes: [LABELED_EVENT]) {
        nodes {
          ... on LabeledEvent {
            createdAt
            label { name }
          }
        }
      }
    }`, alias, varName))
	}

	query := fmt.Sprintf(`query(%s) {
  repository(owner: $owner, name: $repo) {
%s
  }
}`, strings.Join(varDecls, ", "), strings.Join(fragments, "\n"))

	// Execute the query.
	var rawData struct {
		Repository map[string]json.RawMessage `json:"-"`
	}
	var repoData map[string]json.RawMessage

	if err := c.GraphQL(ctx, "batch-fetch-status", query, vars, &struct {
		Repository *map[string]json.RawMessage `json:"repository"`
	}{Repository: &repoData}); err != nil {
		return nil, fmt.Errorf("batch fetch item status: %w", err)
	}
	_ = rawData // suppress unused

	results := make(map[int]ItemStatus, len(issueNumbers))

	for i := range issueNumbers {
		alias := fmt.Sprintf("i%d", i)
		raw, ok := repoData[alias]
		if !ok {
			continue
		}

		var issue struct {
			Number       int `json:"number"`
			ProjectItems struct {
				Nodes []struct {
					ID        string    `json:"id"`
					UpdatedAt time.Time `json:"updatedAt"`
					Project   struct {
						ID string `json:"id"`
					} `json:"project"`
					FieldValues struct {
						Nodes []struct {
							Name      string    `json:"name"`
							UpdatedAt time.Time `json:"updatedAt"`
							Field     struct {
								Name string `json:"name"`
							} `json:"field"`
						} `json:"nodes"`
					} `json:"fieldValues"`
				} `json:"nodes"`
			} `json:"projectItems"`
			TimelineItems struct {
				Nodes []struct {
					CreatedAt time.Time `json:"createdAt"`
					Label     struct {
						Name string `json:"name"`
					} `json:"label"`
				} `json:"nodes"`
			} `json:"timelineItems"`
		}

		if err := json.Unmarshal(raw, &issue); err != nil {
			log.Printf("::warning::Failed to unmarshal issue alias %s: %v", alias, err)
			continue
		}

		if issue.Number == 0 {
			continue
		}

		status := ItemStatus{
			LabelEvents: make(map[string]time.Time),
		}

		// Find the project item matching our target project.
		for _, pi := range issue.ProjectItems.Nodes {
			if pi.Project.ID != projectID {
				continue
			}
			status.ItemID = pi.ID
			status.UpdatedAt = pi.UpdatedAt

			for _, fv := range pi.FieldValues.Nodes {
				if fv.Field.Name == "Status" && fv.Name != "" {
					status.BoardStatus = fv.Name
					if !fv.UpdatedAt.IsZero() {
						status.UpdatedAt = fv.UpdatedAt
					}
					break
				}
			}
			break
		}

		// Collect label timeline events.
		for _, ev := range issue.TimelineItems.Nodes {
			if ev.Label.Name != "" {
				existing, ok := status.LabelEvents[ev.Label.Name]
				if !ok || ev.CreatedAt.After(existing) {
					status.LabelEvents[ev.Label.Name] = ev.CreatedAt
				}
			}
		}

		results[issue.Number] = status
	}

	return results, nil
}

// FetchSyncData orchestrates the two-phase fetch:
// 1. Search API to get open issues in the project
// 2. Batch GraphQL to enrich with project item status and label events
//
// Returns the same []ProjectItem that downstream Reconcile/Execute expect.
func (c *Client) FetchSyncData(ctx context.Context, projectID, projectOwner string, projectNumber int, labelPrefix string) ([]ProjectItem, error) {
	// Phase 1: Search API for open issues.
	searchResults, err := c.SearchOpenIssuesInProject(ctx, projectOwner, projectNumber)
	if err != nil {
		return nil, fmt.Errorf("search open issues: %w", err)
	}

	if len(searchResults) == 0 {
		log.Printf("::notice::No open issues found in project %s/%d", projectOwner, projectNumber)
		return nil, nil
	}

	// Group issues by repo for batch GraphQL calls.
	type repoKey struct {
		Owner string
		Name  string
	}
	issuesByRepo := make(map[repoKey][]SearchResult)
	for _, sr := range searchResults {
		key := repoKey{Owner: sr.RepoOwner, Name: sr.RepoName}
		issuesByRepo[key] = append(issuesByRepo[key], sr)
	}

	// Phase 2: Batch GraphQL to get project item status + label events.
	allStatuses := make(map[repoKey]map[int]ItemStatus)
	totalGraphQLCalls := 0

	for key, issues := range issuesByRepo {
		numbers := make([]int, len(issues))
		for i, iss := range issues {
			numbers[i] = iss.Number
		}

		repoStatuses := make(map[int]ItemStatus)

		// Process in batches of batchSize.
		for start := 0; start < len(numbers); start += batchSize {
			end := start + batchSize
			if end > len(numbers) {
				end = len(numbers)
			}
			batch := numbers[start:end]

			statuses, err := c.BatchFetchItemStatus(ctx, projectID, batch, key.Owner, key.Name)
			if err != nil {
				return nil, fmt.Errorf("batch fetch status for %s/%s: %w", key.Owner, key.Name, err)
			}
			totalGraphQLCalls++

			for num, st := range statuses {
				repoStatuses[num] = st
			}
		}

		allStatuses[key] = repoStatuses
	}

	// Phase 3: Merge search results + item statuses into ProjectItems.
	var items []ProjectItem
	for _, sr := range searchResults {
		key := repoKey{Owner: sr.RepoOwner, Name: sr.RepoName}
		status, ok := allStatuses[key][sr.Number]
		if !ok {
			// Issue wasn't found in the project items — skip.
			continue
		}

		// Filter label events to only those matching the prefix.
		filteredEvents := make(map[string]time.Time)
		for name, t := range status.LabelEvents {
			if strings.HasPrefix(name, labelPrefix) {
				filteredEvents[name] = t
			}
		}

		items = append(items, ProjectItem{
			ItemID:      status.ItemID,
			UpdatedAt:   status.UpdatedAt,
			BoardStatus: status.BoardStatus,
			IssueNumber: sr.Number,
			IssueState:  "OPEN", // Search API only returns open issues.
			RepoOwner:   sr.RepoOwner,
			RepoName:    sr.RepoName,
			Labels:      sr.Labels,
			LabelEvents: filteredEvents,
		})
	}

	searchPages := (len(searchResults) + 99) / 100
	if searchPages < 1 {
		searchPages = 1
	}
	log.Printf("::notice::Fetched %d open issues via search (%d pages), enriched with %d GraphQL calls",
		len(items), searchPages, totalGraphQLCalls)
	return items, nil
}

// UpdateItemStatus sets the Status field value on a project item.
func (c *Client) UpdateItemStatus(ctx context.Context, projectID, itemID, fieldID, optionID string) error {
	const mutation = `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(
    input: {
      projectId: $projectId
      itemId: $itemId
      fieldId: $fieldId
      value: { singleSelectOptionId: $optionId }
    }
  ) {
    projectV2Item { id }
  }
}`

	vars := map[string]any{
		"projectId": projectID,
		"itemId":    itemID,
		"fieldId":   fieldID,
		"optionId":  optionID,
	}

	return c.GraphQL(ctx, "update-item-status", mutation, vars, nil)
}

// ParseProjectURL extracts the owner type ("users" or "orgs"), login,
// and project number from a GitHub Projects v2 URL. It is exported so
// main.go can extract the owner and number for the search query.
func ParseProjectURL(rawURL string) (ownerType, login string, number int, err error) {
	return parseProjectURL(rawURL)
}

// parseProjectURL extracts the owner type ("users" or "orgs"), login,
// and project number from a GitHub Projects v2 URL.
func parseProjectURL(rawURL string) (ownerType, login string, number int, err error) {
	// Expected: https://github.com/users/<login>/projects/<number>
	//       or: https://github.com/orgs/<login>/projects/<number>
	rawURL = strings.TrimRight(rawURL, "/")
	parts := strings.Split(rawURL, "/")

	// Find "projects" segment and work backwards.
	for i, p := range parts {
		if p == "projects" && i >= 2 && i+1 < len(parts) {
			ownerType = parts[i-2]
			login = parts[i-1]
			number, err = strconv.Atoi(parts[i+1])
			if err != nil {
				return "", "", 0, fmt.Errorf("invalid project number in URL %q: %w", rawURL, err)
			}
			if ownerType != "users" && ownerType != "orgs" {
				return "", "", 0, fmt.Errorf("unexpected owner type %q in URL %q (expected users or orgs)", ownerType, rawURL)
			}
			return ownerType, login, number, nil
		}
	}
	return "", "", 0, fmt.Errorf("cannot parse project URL %q: expected https://github.com/{users,orgs}/<login>/projects/<number>", rawURL)
}
