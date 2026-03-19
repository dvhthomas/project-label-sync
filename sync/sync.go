// Package sync implements the bidirectional reconciliation between
// GitHub Projects v2 status fields and issue labels.
package sync

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	gh "github.com/dvhthomas/project-label-sync/github"
)

// Action describes a mutation that should (or would, in dry-run) be performed.
type Action struct {
	Type        ActionType
	IssueNumber int
	Repo        string
	ItemID      string
	Detail      string // human-readable explanation
}

// ActionType enumerates the kinds of mutations the syncer can perform.
type ActionType int

const (
	ActionNone          ActionType = iota
	ActionAddLabel                 // Add a status label to an issue
	ActionRemoveLabel              // Remove a status label from an issue
	ActionUpdateBoard              // Update the board Status field
	ActionSkip                     // Logged but no mutation needed
)

func (a ActionType) String() string {
	switch a {
	case ActionAddLabel:
		return "add-label"
	case ActionRemoveLabel:
		return "remove-label"
	case ActionUpdateBoard:
		return "update-board"
	case ActionSkip:
		return "skip"
	default:
		return "none"
	}
}

// Syncer holds the configuration and dependencies needed for reconciliation.
type Syncer struct {
	Project      *gh.ProjectInfo
	Client       *gh.Client
	Labels       *gh.LabelManager
	LabelPrefix  string
	DryRun       bool

	// optionsByName maps status name -> option ID for board mutations.
	optionsByName map[string]string
}

// NewSyncer creates a Syncer from the given project info and clients.
func NewSyncer(project *gh.ProjectInfo, client *gh.Client, labels *gh.LabelManager, prefix string, dryRun bool) *Syncer {
	opts := make(map[string]string, len(project.Options))
	for _, o := range project.Options {
		opts[o.Name] = o.ID
	}
	return &Syncer{
		Project:       project,
		Client:        client,
		Labels:        labels,
		LabelPrefix:   prefix,
		DryRun:        dryRun,
		optionsByName: opts,
	}
}

// Run fetches all project items and reconciles each one.
func (s *Syncer) Run(ctx context.Context) error {
	items, err := s.Client.FetchProjectItems(ctx, s.Project.ID, s.LabelPrefix)
	if err != nil {
		return err
	}

	var (
		synced  int
		skipped int
		errors  int
	)

	for _, item := range items {
		actions := s.Reconcile(item)
		for _, a := range actions {
			if a.Type == ActionSkip || a.Type == ActionNone {
				skipped++
				continue
			}
			synced++
			if err := s.Execute(ctx, item, a); err != nil {
				errors++
				log.Printf("::error::Failed to execute %s on %s#%d: %v", a.Type, a.Repo, a.IssueNumber, err)
			}
		}
	}

	log.Printf("::notice::Sync complete: %d items processed, %d actions taken, %d skipped, %d errors",
		len(items), synced, skipped, errors)

	if errors > 0 {
		return fmt.Errorf("%d sync errors occurred", errors)
	}
	return nil
}

// Reconcile determines what actions are needed for a single project item.
// This is a pure function (no side effects) for easy testing.
func (s *Syncer) Reconcile(item gh.ProjectItem) []Action {
	repo := item.RepoRef()
	num := item.IssueNumber

	// Skip closed issues.
	if item.IssueState != "OPEN" {
		return []Action{{
			Type:        ActionSkip,
			IssueNumber: num,
			Repo:        repo,
			Detail:      "issue is closed",
		}}
	}

	// Skip items with no board status.
	if item.BoardStatus == "" {
		return []Action{{
			Type:        ActionSkip,
			IssueNumber: num,
			Repo:        repo,
			Detail:      "no board status set",
		}}
	}

	expectedLabel := s.LabelPrefix + item.BoardStatus
	currentStatusLabels := filterByPrefix(item.Labels, s.LabelPrefix)

	switch {
	case len(currentStatusLabels) == 0:
		// Board has status, issue has no status label -> board wins.
		return []Action{{
			Type:        ActionAddLabel,
			IssueNumber: num,
			Repo:        repo,
			Detail:      fmt.Sprintf("board has %q but no status label; adding %q", item.BoardStatus, expectedLabel),
		}}

	case len(currentStatusLabels) == 1:
		current := currentStatusLabels[0]
		if current == expectedLabel {
			return []Action{{
				Type:        ActionSkip,
				IssueNumber: num,
				Repo:        repo,
				Detail:      fmt.Sprintf("in sync: %q", current),
			}}
		}

		// Conflict: label says one thing, board says another.
		labelTime := item.LabelEvents[current]
		boardTime := item.UpdatedAt

		if labelTime.After(boardTime) {
			// Label is newer -> update board to match.
			statusName := strings.TrimPrefix(current, s.LabelPrefix)
			return []Action{{
				Type:        ActionUpdateBoard,
				IssueNumber: num,
				Repo:        repo,
				ItemID:      item.ItemID,
				Detail: fmt.Sprintf(
					"label %q (at %s) is newer than board %q (at %s); updating board to %q",
					current, labelTime.Format(time.RFC3339),
					item.BoardStatus, boardTime.Format(time.RFC3339),
					statusName),
			}}
		}

		// Board is newer -> update label to match.
		return []Action{
			{
				Type:        ActionRemoveLabel,
				IssueNumber: num,
				Repo:        repo,
				Detail:      fmt.Sprintf("removing stale label %q (board is newer)", current),
			},
			{
				Type:        ActionAddLabel,
				IssueNumber: num,
				Repo:        repo,
				Detail:      fmt.Sprintf("adding label %q to match board status %q", expectedLabel, item.BoardStatus),
			},
		}

	default:
		// Multiple status labels -> clean up, board wins.
		var actions []Action
		for _, l := range currentStatusLabels {
			actions = append(actions, Action{
				Type:        ActionRemoveLabel,
				IssueNumber: num,
				Repo:        repo,
				Detail:      fmt.Sprintf("removing competing label %q", l),
			})
		}
		actions = append(actions, Action{
			Type:        ActionAddLabel,
			IssueNumber: num,
			Repo:        repo,
			Detail:      fmt.Sprintf("adding label %q (board wins over %d competing labels)", expectedLabel, len(currentStatusLabels)),
		})
		return actions
	}
}

// Execute performs a single action.
func (s *Syncer) Execute(ctx context.Context, item gh.ProjectItem, a Action) error {
	log.Printf("[%s] %s#%d: %s", a.Type, a.Repo, a.IssueNumber, a.Detail)

	switch a.Type {
	case ActionAddLabel:
		label := s.LabelPrefix + item.BoardStatus
		// If the action detail indicates which label to add, parse it.
		// For simplicity, we always derive from the board status for add actions
		// unless this is part of a board-wins cleanup — but the expected label
		// is already the board's status.
		if err := s.Labels.EnsureLabelExists(ctx, a.Repo, label); err != nil {
			return err
		}
		return s.Labels.AddLabel(ctx, a.Repo, a.IssueNumber, label)

	case ActionRemoveLabel:
		// Parse the label name from the detail — it's the quoted string.
		label := extractQuotedLabel(a.Detail)
		if label == "" {
			return fmt.Errorf("could not determine label to remove from: %s", a.Detail)
		}
		return s.Labels.RemoveLabel(ctx, a.Repo, a.IssueNumber, label)

	case ActionUpdateBoard:
		statusName := extractBoardTarget(a.Detail)
		optionID, ok := s.optionsByName[statusName]
		if !ok {
			return fmt.Errorf("no board option found for status %q", statusName)
		}
		if s.DryRun {
			log.Printf("[dry-run] Would update board status to %q for item %s", statusName, a.ItemID)
			return nil
		}
		return s.Client.UpdateItemStatus(ctx, s.Project.ID, a.ItemID, s.Project.FieldID, optionID)

	case ActionSkip, ActionNone:
		return nil
	}

	return nil
}

// filterByPrefix returns labels that start with the given prefix.
func filterByPrefix(labels []string, prefix string) []string {
	var result []string
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			result = append(result, l)
		}
	}
	return result
}

// extractQuotedLabel extracts the first double-quoted string from text.
func extractQuotedLabel(s string) string {
	start := strings.Index(s, `"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}

// extractBoardTarget extracts the target board status from an update-board
// action detail string. It looks for the last quoted value after "updating board to".
func extractBoardTarget(s string) string {
	marker := "updating board to "
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(marker):]
	return strings.Trim(rest, `"`)
}
