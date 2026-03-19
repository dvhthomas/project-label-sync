// Package sync implements the bidirectional reconciliation between
// GitHub Projects v2 status fields and issue labels.
package sync

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	gh "github.com/dvhthomas/project-label-sync/github"
	applog "github.com/dvhthomas/project-label-sync/internal/log"
)

// Action describes a mutation that should (or would, in preview mode) be performed.
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

// SyncStats tracks counts of each action during a sync run.
type SyncStats struct {
	Scanned       int
	InSync        int
	LabelsAdded   int
	LabelsRemoved int
	BoardUpdated  int
	LabelsCreated int
	Skipped       int
	Errors        int
}

// Syncer holds the configuration and dependencies needed for reconciliation.
type Syncer struct {
	Project         *gh.ProjectInfo
	Client          *gh.Client
	Labels          LabelSyncer
	Board           BoardUpdater
	Mapping         map[string][]string // field value -> labels
	ReverseMap      map[string]string   // label -> field value
	AllMappedLabels []string            // flat list of all mapped labels
	FieldName       string
	DryRun          bool
	Verbose         bool
	ProjectOwner    string
	ProjectNumber   int
	ProjectURL      string

	// optionsByName maps status name -> option ID for board mutations.
	optionsByName map[string]string

	// Stats tracks action counts during Run().
	Stats SyncStats
}

// NewSyncer creates a Syncer from the given project info, clients, and mapping.
func NewSyncer(project *gh.ProjectInfo, client *gh.Client, labels LabelSyncer, board BoardUpdater, mapping map[string][]string, fieldName string, dryRun bool, projectOwner string, projectNumber int) *Syncer {
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
		Board:           board,
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

// logConfigSummary prints the configuration overview at the start of a run.
func (s *Syncer) logConfigSummary() {
	mode := "LIVE"
	if s.DryRun {
		mode = "Preview (no changes made — use --apply to update issues)"
	}

	projectRef := s.Project.Title
	if s.ProjectURL != "" {
		projectRef = fmt.Sprintf("%s (%s)", s.Project.Title, s.ProjectURL)
	}

	var mappingLines []string
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(s.Mapping))
	for k := range s.Mapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		mappingLines = append(mappingLines, fmt.Sprintf("    %q → [%s]", k, strings.Join(s.Mapping[k], ", ")))
	}

	applog.Notice("Configuration:\n  Project: %s\n  Field: %s\n  Mappings:\n%s\n  Mode: %s",
		projectRef, s.FieldName, strings.Join(mappingLines, "\n"), mode)
}

// logLabelCheck reports which mapped labels exist on the repo and which are missing.
func (s *Syncer) logLabelCheck(ctx context.Context, repos []string) {
	for _, repo := range repos {
		existing, missing, err := s.Labels.CheckLabelsExist(ctx, repo, s.AllMappedLabels)
		if err != nil {
			applog.Warn("Label check on %s failed: %v", repo, err)
			continue
		}

		var lines []string
		for _, l := range existing {
			lines = append(lines, fmt.Sprintf("  ✓ %s (exists)", l))
		}
		for _, l := range missing {
			lines = append(lines, fmt.Sprintf("  ✗ %s (will be created)", l))
		}
		s.Stats.LabelsCreated = len(missing)

		applog.Notice("Label check on %s:\n%s", repo, strings.Join(lines, "\n"))
	}
}

// logSummary prints a summary of all actions taken (or planned) during the run.
func (s *Syncer) logSummary() {
	verb := "Would add labels"
	verbRemove := "Would remove labels"
	verbBoard := "Would update board"
	verbCreate := "Labels to create"
	if !s.DryRun {
		verb = "Labels added"
		verbRemove = "Labels removed"
		verbBoard = "Board updated"
		verbCreate = "Labels created"
	}

	applog.Notice("Summary:\n  Issues scanned: %d\n  Already in sync: %d\n  %s: %d issues\n  %s: %d issues\n  %s: %d issues\n  %s: %d\n  Skipped (unmapped/closed): %d\n  Errors: %d",
		s.Stats.Scanned,
		s.Stats.InSync,
		verb, s.Stats.LabelsAdded,
		verbRemove, s.Stats.LabelsRemoved,
		verbBoard, s.Stats.BoardUpdated,
		verbCreate, s.Stats.LabelsCreated,
		s.Stats.Skipped,
		s.Stats.Errors,
	)
}

// validateMapping checks that every key in the mapping corresponds to an actual
// option on the project's status field. It also warns about unmapped options.
// On failure the error message includes the list of valid options so the user
// can fix their config.
func (s *Syncer) validateMapping() error {
	validOptions := make(map[string]bool, len(s.Project.Options))
	var optionNames []string
	for _, opt := range s.Project.Options {
		validOptions[opt.Name] = true
		optionNames = append(optionNames, opt.Name)
	}
	sort.Strings(optionNames)

	var warnings []string
	var errs []string

	for configValue := range s.Mapping {
		if !validOptions[configValue] {
			errs = append(errs, fmt.Sprintf("mapping contains %q but the project's %s field has no such option", configValue, s.FieldName))
		}
	}

	// Warn about unmapped options (informational, not an error).
	for _, opt := range s.Project.Options {
		if _, mapped := s.Mapping[opt.Name]; !mapped {
			warnings = append(warnings, fmt.Sprintf("project status %q is not mapped (will be ignored)", opt.Name))
		}
	}

	for _, w := range warnings {
		applog.Warn("%s", w)
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		for _, e := range errs {
			applog.Error("%s", e)
		}
		applog.Error("Available options: %s", strings.Join(optionNames, ", "))
		return fmt.Errorf("%d mapping value(s) do not match any project field option — check spelling and capitalization", len(errs))
	}

	return nil
}

// Run fetches all project items via the Search API + batch GraphQL enrichment,
// then reconciles each one.
func (s *Syncer) Run(ctx context.Context) error {
	// Log configuration summary.
	s.logConfigSummary()

	// Validate mapping against actual project field options before doing any work.
	if err := s.validateMapping(); err != nil {
		return err
	}

	items, err := s.Client.FetchSyncData(ctx, s.Project.ID, s.ProjectOwner, s.ProjectNumber, s.FieldName, s.AllMappedLabels)
	if err != nil {
		return err
	}

	s.Stats.Scanned = len(items)

	// Determine unique repos for label check.
	repoSet := make(map[string]bool)
	for _, item := range items {
		repoSet[item.RepoRef()] = true
	}
	repos := make([]string, 0, len(repoSet))
	for r := range repoSet {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	// Pre-flight label check.
	s.logLabelCheck(ctx, repos)

	// Track per-issue action types for summary counting.
	issueAdds := make(map[int]bool)
	issueRemoves := make(map[int]bool)
	issueBoards := make(map[int]bool)

	for _, item := range items {
		actions := s.Reconcile(item)
		for _, a := range actions {
			if a.Type == ActionSkip || a.Type == ActionNone {
				if a.Detail != "" && strings.Contains(a.Detail, "in sync") {
					s.Stats.InSync++
				} else {
					s.Stats.Skipped++
				}
				continue
			}

			if execErr := s.Execute(ctx, item, a); execErr != nil {
				s.Stats.Errors++
				applog.Error("Failed to execute %s on %s#%d: %v", a.Type, a.Repo, a.IssueNumber, execErr)
			} else {
				switch a.Type {
				case ActionAddLabel:
					issueAdds[a.IssueNumber] = true
				case ActionRemoveLabel:
					issueRemoves[a.IssueNumber] = true
				case ActionUpdateBoard:
					issueBoards[a.IssueNumber] = true
				}
			}
		}
	}

	s.Stats.LabelsAdded = len(issueAdds)
	s.Stats.LabelsRemoved = len(issueRemoves)
	s.Stats.BoardUpdated = len(issueBoards)

	// Log final summary.
	s.logSummary()

	if s.Stats.Errors > 0 {
		return fmt.Errorf("%d sync errors occurred", s.Stats.Errors)
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
	if s.Verbose {
		applog.Info("[%s] %s#%d: %s", a.Type, a.Repo, a.IssueNumber, a.Detail)
	}

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
			applog.Preview("Would update board status to %q for item %s", a.StatusName, a.ItemID)
			return nil
		}
		return s.Board.UpdateItemStatus(ctx, s.Project.ID, a.ItemID, s.Project.FieldID, optionID)

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
