// Package sync implements the bidirectional reconciliation between
// GitHub Projects v2 status fields and issue labels.
package sync

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	gh "github.com/dvhthomas/project-label-sync/github"
)

// MutationDelay is the pause between mutation API calls to avoid
// triggering GitHub's secondary rate limits.
const MutationDelay = 500 * time.Millisecond

// Action describes a mutation that should (or would, in dry-run) be performed.
type Action struct {
	Type        ActionType
	IssueNumber int
	Repo        string
	ItemID      string
	Label       string // Label to add/remove (for label actions)
	StatusName  string // Target board status (for board actions)
	Detail      string // human-readable explanation (logging only)
}

// ActionType enumerates the kinds of mutations the syncer can perform.
type ActionType int

const (
	ActionNone        ActionType = iota
	ActionAddLabel               // Add a status label to an issue
	ActionRemoveLabel            // Remove a status label from an issue
	ActionUpdateBoard            // Update the board Status field
	ActionSkip                   // Logged but no mutation needed
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
	Project         *gh.ProjectInfo
	Client          *gh.Client
	Labels          *gh.LabelManager
	Mapping         map[string][]string // field value -> labels
	ReverseMap      map[string]string   // label -> field value
	AllMappedLabels []string            // flat list of all mapped labels
	FieldName       string
	DryRun          bool
	ProjectOwner    string
	ProjectNumber   int

	// optionsByName maps status name -> option ID for board mutations.
	optionsByName map[string]string
}

// NewSyncer creates a Syncer from the given project info, clients, and mapping.
func NewSyncer(project *gh.ProjectInfo, client *gh.Client, labels *gh.LabelManager, mapping map[string][]string, fieldName string, dryRun bool, projectOwner string, projectNumber int) *Syncer {
	opts := make(map[string]string, len(project.Options))
	for _, o := range project.Options {
		opts[o.Name] = o.ID
	}

	reverseMap := make(map[string]string)
	var allLabels []string
	for fieldValue, lbls := range mapping {
		for _, l := range lbls {
			reverseMap[l] = fieldValue
			allLabels = append(allLabels, l)
		}
	}
	sort.Strings(allLabels)

	return &Syncer{
		Project:         project,
		Client:          client,
		Labels:          labels,
		Mapping:         mapping,
		ReverseMap:      reverseMap,
		AllMappedLabels: allLabels,
		FieldName:       fieldName,
		DryRun:          dryRun,
		ProjectOwner:    projectOwner,
		ProjectNumber:   projectNumber,
		optionsByName:   opts,
	}
}

// Run fetches all project items via the Search API + batch GraphQL enrichment,
// then reconciles each one.
func (s *Syncer) Run(ctx context.Context) error {
	items, err := s.Client.FetchSyncData(ctx, s.Project.ID, s.ProjectOwner, s.ProjectNumber, s.FieldName, s.AllMappedLabels)
	if err != nil {
		return err
	}

	var (
		synced       int
		skipped      int
		errors       int
		labelChanges int
		boardUpdates int
		firstMut     = true
	)

	for _, item := range items {
		actions := s.Reconcile(item)
		for _, a := range actions {
			if a.Type == ActionSkip || a.Type == ActionNone {
				skipped++
				continue
			}

			// Throttle between mutations to avoid secondary rate limits.
			if !s.DryRun && !firstMut {
				time.Sleep(MutationDelay)
			}
			firstMut = false

			synced++
			if err := s.Execute(ctx, item, a); err != nil {
				errors++
				log.Printf("::error::Failed to execute %s on %s#%d: %v", a.Type, a.Repo, a.IssueNumber, err)
			} else {
				switch a.Type {
				case ActionAddLabel, ActionRemoveLabel:
					labelChanges++
				case ActionUpdateBoard:
					boardUpdates++
				}
			}
		}
	}

	log.Printf("::notice::Sync complete: %d items processed, %d actions taken, %d skipped, %d errors",
		len(items), synced, skipped, errors)

	// Log API budget summary.
	log.Printf("::notice::API budget: %d GraphQL points used, %d remaining",
		s.Client.PointsUsed, s.Client.RateLimitRemaining())
	log.Printf("::notice::Mutations: %d label changes, %d board updates",
		labelChanges, boardUpdates)

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

	// Look up expected labels from the mapping.
	expectedLabels, mapped := s.Mapping[item.BoardStatus]
	if !mapped {
		return []Action{{
			Type:        ActionSkip,
			IssueNumber: num,
			Repo:        repo,
			Detail:      fmt.Sprintf("board status %q not in mapping", item.BoardStatus),
		}}
	}

	currentMappedLabels := filterToMapped(item.Labels, s.AllMappedLabels)

	// Case 1: No mapped labels on issue → board wins, add all expected.
	if len(currentMappedLabels) == 0 {
		var actions []Action
		for _, lbl := range expectedLabels {
			actions = append(actions, Action{
				Type:        ActionAddLabel,
				IssueNumber: num,
				Repo:        repo,
				Label:       lbl,
				Detail:      fmt.Sprintf("board has %q but no mapped label; adding %q", item.BoardStatus, lbl),
			})
		}
		return actions
	}

	// Case 2: Current labels exactly match expected → in sync.
	if sameSet(currentMappedLabels, expectedLabels) {
		return []Action{{
			Type:        ActionSkip,
			IssueNumber: num,
			Repo:        repo,
			Detail:      fmt.Sprintf("in sync: %v", expectedLabels),
		}}
	}

	// Case 3+4: Determine if current labels all belong to one status.
	currentStatus := inferStatus(currentMappedLabels, s.ReverseMap)

	if currentStatus == "" {
		// Labels span multiple statuses → cleanup, board wins.
		return s.boardWins(num, repo, item, currentMappedLabels, expectedLabels)
	}

	// All current labels point to a single status that differs from the board.
	// Resolve conflict via timestamps.
	labelTime := latestLabelTime(currentMappedLabels, item.LabelEvents)
	boardTime := item.UpdatedAt

	if labelTime.After(boardTime) {
		// Labels win → update board to match the label status.
		return []Action{{
			Type:        ActionUpdateBoard,
			IssueNumber: num,
			Repo:        repo,
			ItemID:      item.ItemID,
			StatusName:  currentStatus,
			Detail: fmt.Sprintf(
				"labels %v (at %s) are newer than board %q (at %s); updating board to %q",
				currentMappedLabels, labelTime.Format("2006-01-02T15:04:05Z"),
				item.BoardStatus, boardTime.Format("2006-01-02T15:04:05Z"),
				currentStatus),
		}}
	}

	// Board wins → remove current labels, add expected labels.
	return s.boardWins(num, repo, item, currentMappedLabels, expectedLabels)
}

// boardWins removes all current mapped labels and adds the expected ones.
func (s *Syncer) boardWins(num int, repo string, item gh.ProjectItem, currentMappedLabels, expectedLabels []string) []Action {
	var actions []Action
	for _, l := range currentMappedLabels {
		actions = append(actions, Action{
			Type:        ActionRemoveLabel,
			IssueNumber: num,
			Repo:        repo,
			Label:       l,
			Detail:      fmt.Sprintf("removing competing label %q", l),
		})
	}
	for _, lbl := range expectedLabels {
		actions = append(actions, Action{
			Type:        ActionAddLabel,
			IssueNumber: num,
			Repo:        repo,
			Label:       lbl,
			Detail:      fmt.Sprintf("adding label %q (board wins over %d competing labels)", lbl, len(currentMappedLabels)),
		})
	}
	return actions
}

// Execute performs a single action.
func (s *Syncer) Execute(ctx context.Context, _ gh.ProjectItem, a Action) error {
	log.Printf("[%s] %s#%d: %s", a.Type, a.Repo, a.IssueNumber, a.Detail)

	switch a.Type {
	case ActionAddLabel:
		if err := s.Labels.EnsureLabelExists(ctx, a.Repo, a.Label); err != nil {
			return err
		}
		return s.Labels.AddLabel(ctx, a.Repo, a.IssueNumber, a.Label)

	case ActionRemoveLabel:
		return s.Labels.RemoveLabel(ctx, a.Repo, a.IssueNumber, a.Label)

	case ActionUpdateBoard:
		optionID, ok := s.optionsByName[a.StatusName]
		if !ok {
			return fmt.Errorf("no board option found for status %q", a.StatusName)
		}
		if s.DryRun {
			log.Printf("[dry-run] Would update board status to %q for item %s", a.StatusName, a.ItemID)
			return nil
		}
		return s.Client.UpdateItemStatus(ctx, s.Project.ID, a.ItemID, s.Project.FieldID, optionID)

	case ActionSkip, ActionNone:
		return nil
	}

	return nil
}

// sameSet returns true if both slices contain the same elements (order-independent).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		if !set[v] {
			return false
		}
	}
	return true
}

// inferStatus returns the status name if all labels map to the same status
// via the reverse map. Returns empty string if labels map to different statuses
// or if any label is not found in the reverse map.
func inferStatus(labels []string, reverseMap map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	status := ""
	for _, l := range labels {
		s, ok := reverseMap[l]
		if !ok {
			return ""
		}
		if status == "" {
			status = s
		} else if status != s {
			return ""
		}
	}
	return status
}

// latestLabelTime returns the most recent event time across the given labels.
func latestLabelTime(labels []string, events map[string]time.Time) time.Time {
	var latest time.Time
	for _, l := range labels {
		if t, ok := events[l]; ok && t.After(latest) {
			latest = t
		}
	}
	return latest
}

// filterToMapped returns labels that are present in the allMappedLabels list.
func filterToMapped(labels []string, allMappedLabels []string) []string {
	set := make(map[string]bool, len(allMappedLabels))
	for _, l := range allMappedLabels {
		set[l] = true
	}
	var result []string
	for _, l := range labels {
		if set[l] {
			result = append(result, l)
		}
	}
	return result
}
