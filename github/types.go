// Package github provides GraphQL and REST client wrappers for
// interacting with GitHub Projects v2 and issue labels.
package github

import "time"

// ProjectInfo holds the resolved project metadata needed for sync.
type ProjectInfo struct {
	ID      string         // Global node ID of the project
	Title   string         // Human-readable project title
	FieldID string         // Node ID of the Status field
	Options []StatusOption // Available status values
}

// StatusOption is one selectable value in the Status single-select field.
type StatusOption struct {
	ID   string // Option node ID (used in mutations)
	Name string // Display name (e.g. "In Progress")
}

// ProjectItem represents a single item on the project board that is
// backed by an issue (draft items and PRs are skipped).
type ProjectItem struct {
	ItemID      string    // Project item node ID
	UpdatedAt   time.Time // Last update time of the project item
	BoardStatus string    // Current Status field value (may be empty)

	IssueNumber int      // Issue number in the repository
	IssueState  string   // OPEN or CLOSED
	RepoOwner   string   // Repository owner
	RepoName    string   // Repository name
	Labels      []string // Current issue label names

	// LabelEvents maps label name -> most recent LabeledEvent time.
	// Only populated for labels matching the configured prefix.
	LabelEvents map[string]time.Time
}

// RepoRef returns "owner/repo" for gh CLI commands.
func (p ProjectItem) RepoRef() string {
	return p.RepoOwner + "/" + p.RepoName
}
