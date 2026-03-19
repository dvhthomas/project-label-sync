package sync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	gh "github.com/dvhthomas/project-label-sync/github"
)

// mockLabels records label operations and optionally fails on specific labels.
type mockLabels struct {
	ensured []string            // labels that were "created"
	added   map[string][]string // "repo#number" -> labels added
	removed map[string][]string // "repo#number" -> labels removed
	checked map[string]bool     // labels that "exist" on the repo
	failOn  string              // label name that should cause an error
	hasFail bool                // whether failOn is set (distinguishes from empty string)
}

func newMockLabels() *mockLabels {
	return &mockLabels{
		added:   make(map[string][]string),
		removed: make(map[string][]string),
		checked: make(map[string]bool),
	}
}

func (m *mockLabels) EnsureLabelExists(_ context.Context, repo, labelName string) error {
	if m.hasFail && m.failOn == labelName {
		return fmt.Errorf("ensure failed for %q", labelName)
	}
	m.ensured = append(m.ensured, labelName)
	return nil
}

func (m *mockLabels) AddLabel(_ context.Context, repo string, issueNumber int, labelName string) error {
	if m.hasFail && m.failOn == labelName {
		return fmt.Errorf("add failed for %q", labelName)
	}
	key := fmt.Sprintf("%s#%d", repo, issueNumber)
	m.added[key] = append(m.added[key], labelName)
	return nil
}

func (m *mockLabels) RemoveLabel(_ context.Context, repo string, issueNumber int, labelName string) error {
	if m.hasFail && m.failOn == labelName {
		return fmt.Errorf("remove failed for %q", labelName)
	}
	key := fmt.Sprintf("%s#%d", repo, issueNumber)
	m.removed[key] = append(m.removed[key], labelName)
	return nil
}

func (m *mockLabels) CheckLabelsExist(_ context.Context, repo string, labels []string) (existing, missing []string, err error) {
	for _, l := range labels {
		if m.checked[l] {
			existing = append(existing, l)
		} else {
			missing = append(missing, l)
		}
	}
	return existing, missing, nil
}

// mockBoard records board update calls and optionally fails.
type mockBoard struct {
	updates []boardUpdate
	failOn  string // optionID that should cause an error
}

type boardUpdate struct {
	projectID, itemID, fieldID, optionID string
}

func (m *mockBoard) UpdateItemStatus(_ context.Context, projectID, itemID, fieldID, optionID string) error {
	if m.failOn == optionID {
		return fmt.Errorf("board update failed for option %q", optionID)
	}
	m.updates = append(m.updates, boardUpdate{projectID, itemID, fieldID, optionID})
	return nil
}

// helper to build a test Syncer with mocks.
func testSyncer(labels LabelSyncer, board BoardUpdater, dryRun bool) *Syncer {
	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test Project",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "Todo"},
			{ID: "opt2", Name: "In Progress"},
			{ID: "opt3", Name: "Done"},
		},
	}
	mapping := map[string][]string{
		"Todo":        {"todo"},
		"In Progress": {"in-progress"},
		"Done":        {"done"},
	}
	return NewSyncer(project, nil, labels, board, mapping, "Status", dryRun, "testowner", 1)
}

func dummyItem() gh.ProjectItem {
	return gh.ProjectItem{
		ItemID:      "item1",
		IssueNumber: 42,
		IssueState:  "OPEN",
		RepoOwner:   "owner",
		RepoName:    "repo",
	}
}

func TestExecuteAddLabel_HappyPath(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)
	ctx := context.Background()

	action := Action{
		Type:        ActionAddLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "in-progress",
		Detail:      "test add",
	}

	err := s.Execute(ctx, dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ml.ensured) != 1 || ml.ensured[0] != "in-progress" {
		t.Errorf("ensured = %v, want [in-progress]", ml.ensured)
	}
	added := ml.added["owner/repo#42"]
	if len(added) != 1 || added[0] != "in-progress" {
		t.Errorf("added = %v, want [in-progress]", added)
	}
}

func TestExecuteAddLabel_EnsureFails(t *testing.T) {
	ml := newMockLabels()
	ml.failOn = "in-progress"
	ml.hasFail = true
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:        ActionAddLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "in-progress",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err == nil {
		t.Fatal("expected error when EnsureLabelExists fails")
	}
	// AddLabel should NOT have been called.
	if len(ml.added) != 0 {
		t.Errorf("AddLabel should not be called when EnsureLabelExists fails, got %v", ml.added)
	}
}

func TestExecuteAddLabel_AddFails(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	// EnsureLabelExists succeeds, but AddLabel fails.
	// Use a special label name for failure.
	ml.failOn = "" // EnsureLabelExists succeeds for all
	action := Action{
		Type:        ActionAddLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "fail-add",
	}

	// Set failOn after ensure to only fail on AddLabel.
	// Actually, our mock fails on both if failOn matches. Let's adjust:
	// We need a mock that fails only on AddLabel. Let's use a wrapper.
	addFailLabels := &addFailMockLabels{mockLabels: newMockLabels()}
	s.Labels = addFailLabels

	err := s.Execute(context.Background(), dummyItem(), action)
	if err == nil {
		t.Fatal("expected error when AddLabel fails")
	}
	if len(addFailLabels.ensured) != 1 {
		t.Errorf("EnsureLabelExists should have been called, ensured = %v", addFailLabels.ensured)
	}
}

// addFailMockLabels always fails on AddLabel but succeeds on everything else.
type addFailMockLabels struct {
	*mockLabels
}

func (m *addFailMockLabels) AddLabel(_ context.Context, repo string, issueNumber int, labelName string) error {
	return errors.New("add label network error")
}

func TestExecuteRemoveLabel_HappyPath(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:        ActionRemoveLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "todo",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	removed := ml.removed["owner/repo#42"]
	if len(removed) != 1 || removed[0] != "todo" {
		t.Errorf("removed = %v, want [todo]", removed)
	}
}

func TestExecuteRemoveLabel_Fails(t *testing.T) {
	ml := newMockLabels()
	ml.failOn = "todo"
	ml.hasFail = true
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:        ActionRemoveLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "todo",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err == nil {
		t.Fatal("expected error when RemoveLabel fails")
	}
}

func TestExecuteUpdateBoard_HappyPath(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:        ActionUpdateBoard,
		IssueNumber: 42,
		Repo:        "owner/repo",
		ItemID:      "item1",
		StatusName:  "In Progress",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mb.updates) != 1 {
		t.Fatalf("expected 1 board update, got %d", len(mb.updates))
	}
	u := mb.updates[0]
	if u.projectID != "PVT_test" || u.itemID != "item1" || u.fieldID != "PVTSSF_test" || u.optionID != "opt2" {
		t.Errorf("board update = %+v, want {PVT_test, item1, PVTSSF_test, opt2}", u)
	}
}

func TestExecuteUpdateBoard_UnknownStatus(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:        ActionUpdateBoard,
		IssueNumber: 42,
		Repo:        "owner/repo",
		ItemID:      "item1",
		StatusName:  "Unknown Status",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err == nil {
		t.Fatal("expected error for unknown status name")
	}
	if len(mb.updates) != 0 {
		t.Errorf("should not have called UpdateItemStatus")
	}
}

func TestExecuteUpdateBoard_DryRun(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, true) // DryRun = true

	action := Action{
		Type:        ActionUpdateBoard,
		IssueNumber: 42,
		Repo:        "owner/repo",
		ItemID:      "item1",
		StatusName:  "In Progress",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mb.updates) != 0 {
		t.Errorf("UpdateItemStatus should NOT be called in dry-run mode, got %d calls", len(mb.updates))
	}
}

func TestExecuteSkip(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:   ActionSkip,
		Detail: "nothing to do",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ml.ensured) != 0 || len(ml.added) != 0 || len(ml.removed) != 0 || len(mb.updates) != 0 {
		t.Error("skip should not call any mutation methods")
	}
}

func TestExecuteNone(t *testing.T) {
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type: ActionNone,
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteAddLabel_DryRunStillCallsLabels(t *testing.T) {
	// In the current design, dry-run is handled inside LabelManager,
	// not in Execute. So Execute still calls Labels methods.
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, true) // DryRun = true

	action := Action{
		Type:        ActionAddLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "in-progress",
	}

	err := s.Execute(context.Background(), dummyItem(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Labels methods should still be called (dry-run is handled inside them).
	if len(ml.ensured) != 1 {
		t.Errorf("EnsureLabelExists should be called even in dry-run, got %d calls", len(ml.ensured))
	}
	if len(ml.added["owner/repo#42"]) != 1 {
		t.Errorf("AddLabel should be called even in dry-run, got %v", ml.added)
	}
}

func TestExecuteMultipleAddLabels(t *testing.T) {
	// Simulates two ActionAddLabel from a single reconciliation
	// (e.g., status maps to 2 labels).
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	actions := []Action{
		{Type: ActionAddLabel, IssueNumber: 42, Repo: "owner/repo", Label: "in-progress"},
		{Type: ActionAddLabel, IssueNumber: 42, Repo: "owner/repo", Label: "active"},
	}

	for _, a := range actions {
		if err := s.Execute(context.Background(), dummyItem(), a); err != nil {
			t.Fatalf("unexpected error on %q: %v", a.Label, err)
		}
	}

	if len(ml.ensured) != 2 {
		t.Errorf("ensured = %v, want 2 items", ml.ensured)
	}
	added := ml.added["owner/repo#42"]
	if len(added) != 2 {
		t.Errorf("added = %v, want 2 items", added)
	}
}

func TestExecuteRemoveLabel_EmptyLabel(t *testing.T) {
	// If Label is empty on a remove action, RemoveLabel still gets called.
	// This tests current behavior -- the label manager would handle an empty name.
	ml := newMockLabels()
	mb := &mockBoard{}
	s := testSyncer(ml, mb, false)

	action := Action{
		Type:        ActionRemoveLabel,
		IssueNumber: 42,
		Repo:        "owner/repo",
		Label:       "", // empty!
	}

	// Execute does not guard against empty label -- it delegates to Labels.
	err := s.Execute(context.Background(), dummyItem(), action)
	// This should succeed (mock doesn't care about empty string).
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	removed := ml.removed["owner/repo#42"]
	if len(removed) != 1 || removed[0] != "" {
		t.Errorf("removed = %v, want [\"\"]", removed)
	}
}
