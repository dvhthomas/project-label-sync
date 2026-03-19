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

// FetchProjectItems pages through all items in the project and returns
// those backed by open issues. The labelPrefix is used to filter
// timeline events to only relevant label events.
func (c *Client) FetchProjectItems(ctx context.Context, projectID string, labelPrefix string) ([]ProjectItem, error) {
	const query = `query($projectId: ID!, $cursor: String) {
  node(id: $projectId) {
    ... on ProjectV2 {
      items(first: 50, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          updatedAt
          fieldValues(first: 20) {
            nodes {
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                updatedAt
                field { ... on ProjectV2SingleSelectField { name } }
              }
            }
          }
          content {
            ... on Issue {
              number
              state
              labels(first: 20) { nodes { name } }
              repository { nameWithOwner }
              timelineItems(last: 50, itemTypes: [LABELED_EVENT]) {
                nodes {
                  ... on LabeledEvent {
                    createdAt
                    label { name }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

	var allItems []ProjectItem
	var cursor *string

	for {
		vars := map[string]any{"projectId": projectID}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		var data struct {
			Node struct {
				Items struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID          string    `json:"id"`
						UpdatedAt   time.Time `json:"updatedAt"`
						FieldValues struct {
							Nodes []struct {
								Name      string    `json:"name"`
								UpdatedAt time.Time `json:"updatedAt"`
								Field     struct {
									Name string `json:"name"`
								} `json:"field"`
							} `json:"nodes"`
						} `json:"fieldValues"`
						Content *struct {
							Number int    `json:"number"`
							State  string `json:"state"`
							Labels struct {
								Nodes []struct {
									Name string `json:"name"`
								} `json:"nodes"`
							} `json:"labels"`
							Repository struct {
								NameWithOwner string `json:"nameWithOwner"`
							} `json:"repository"`
							TimelineItems struct {
								Nodes []struct {
									CreatedAt time.Time `json:"createdAt"`
									Label     struct {
										Name string `json:"name"`
									} `json:"label"`
								} `json:"nodes"`
							} `json:"timelineItems"`
						} `json:"content"`
					} `json:"nodes"`
				} `json:"items"`
			} `json:"node"`
		}

		if err := c.GraphQL(ctx, "fetch-items", query, vars, &data); err != nil {
			return nil, fmt.Errorf("fetch project items: %w", err)
		}

		for _, node := range data.Node.Items.Nodes {
			// Skip items that are not issues (drafts, PRs).
			if node.Content == nil || node.Content.Number == 0 {
				continue
			}

			content := node.Content

			// Extract repo owner and name.
			parts := strings.SplitN(content.Repository.NameWithOwner, "/", 2)
			if len(parts) != 2 {
				continue
			}

			// Find the Status field value.
			var boardStatus string
			var boardUpdatedAt time.Time
			for _, fv := range node.FieldValues.Nodes {
				if fv.Field.Name == "Status" && fv.Name != "" {
					boardStatus = fv.Name
					boardUpdatedAt = fv.UpdatedAt
					break
				}
			}
			// Fall back to item-level updatedAt if field-level is zero.
			if boardUpdatedAt.IsZero() {
				boardUpdatedAt = node.UpdatedAt
			}

			// Collect labels.
			var labels []string
			for _, l := range content.Labels.Nodes {
				labels = append(labels, l.Name)
			}

			// Collect label events matching prefix.
			labelEvents := make(map[string]time.Time)
			for _, ev := range content.TimelineItems.Nodes {
				if ev.Label.Name != "" && strings.HasPrefix(ev.Label.Name, labelPrefix) {
					existing, ok := labelEvents[ev.Label.Name]
					if !ok || ev.CreatedAt.After(existing) {
						labelEvents[ev.Label.Name] = ev.CreatedAt
					}
				}
			}

			allItems = append(allItems, ProjectItem{
				ItemID:      node.ID,
				UpdatedAt:   boardUpdatedAt,
				BoardStatus: boardStatus,
				IssueNumber: content.Number,
				IssueState:  content.State,
				RepoOwner:   parts[0],
				RepoName:    parts[1],
				Labels:      labels,
				LabelEvents: labelEvents,
			})
		}

		if !data.Node.Items.PageInfo.HasNextPage {
			break
		}
		endCursor := data.Node.Items.PageInfo.EndCursor
		cursor = &endCursor
	}

	log.Printf("::notice::Fetched %d issue-backed items from project", len(allItems))
	return allItems, nil
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
