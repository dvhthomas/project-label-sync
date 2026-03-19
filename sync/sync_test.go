package sync

import (
	"testing"
	"time"

	gh "github.com/dvhthomas/project-label-sync/github"
)

func TestReconcile(t *testing.T) {
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-1 * time.Hour)
	later := now.Add(1 * time.Hour)

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

	syncer := NewSyncer(project, nil, nil, "status:", false)

	tests := []struct {
		name       string
		item       gh.ProjectItem
		wantLen    int
		wantType   []ActionType
		wantLabels []string // expected Label field per action (empty string = don't check)
		wantStatus []string // expected StatusName field per action
	}{
		{
			name: "board has status, no label -> add label",
			item: gh.ProjectItem{
				ItemID:      "item1",
				UpdatedAt:   now,
				BoardStatus: "In Progress",
				IssueNumber: 1,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"bug"},
				LabelEvents: map[string]time.Time{},
			},
			wantLen:    1,
			wantType:   []ActionType{ActionAddLabel},
			wantLabels: []string{"status:In Progress"},
		},
		{
			name: "label matches board -> no action",
			item: gh.ProjectItem{
				ItemID:      "item2",
				UpdatedAt:   now,
				BoardStatus: "In Progress",
				IssueNumber: 2,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"status:In Progress", "bug"},
				LabelEvents: map[string]time.Time{
					"status:In Progress": now,
				},
			},
			wantLen:    1,
			wantType:   []ActionType{ActionSkip},
			wantLabels: []string{""},
		},
		{
			name: "label differs from board, label newer -> update board",
			item: gh.ProjectItem{
				ItemID:      "item3",
				UpdatedAt:   earlier,
				BoardStatus: "Todo",
				IssueNumber: 3,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"status:In Progress"},
				LabelEvents: map[string]time.Time{
					"status:In Progress": later,
				},
			},
			wantLen:    1,
			wantType:   []ActionType{ActionUpdateBoard},
			wantStatus: []string{"In Progress"},
		},
		{
			name: "label differs from board, board newer -> update label",
			item: gh.ProjectItem{
				ItemID:      "item4",
				UpdatedAt:   later,
				BoardStatus: "In Progress",
				IssueNumber: 4,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"status:Todo"},
				LabelEvents: map[string]time.Time{
					"status:Todo": earlier,
				},
			},
			wantLen:    2,
			wantType:   []ActionType{ActionRemoveLabel, ActionAddLabel},
			wantLabels: []string{"status:Todo", "status:In Progress"},
		},
		{
			name: "multiple status labels -> clean up, board wins",
			item: gh.ProjectItem{
				ItemID:      "item5",
				UpdatedAt:   now,
				BoardStatus: "In Progress",
				IssueNumber: 5,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"status:Todo", "status:Done", "bug"},
				LabelEvents: map[string]time.Time{
					"status:Todo": earlier,
					"status:Done": earlier,
				},
			},
			wantLen:    3, // remove Todo, remove Done, add In Progress
			wantType:   []ActionType{ActionRemoveLabel, ActionRemoveLabel, ActionAddLabel},
			wantLabels: []string{"status:Todo", "status:Done", "status:In Progress"},
		},
		{
			name: "closed issue -> skip",
			item: gh.ProjectItem{
				ItemID:      "item6",
				UpdatedAt:   now,
				BoardStatus: "Done",
				IssueNumber: 6,
				IssueState:  "CLOSED",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{},
				LabelEvents: map[string]time.Time{},
			},
			wantLen:    1,
			wantType:   []ActionType{ActionSkip},
			wantLabels: []string{""},
		},
		{
			name: "empty board status -> skip",
			item: gh.ProjectItem{
				ItemID:      "item7",
				UpdatedAt:   now,
				BoardStatus: "",
				IssueNumber: 7,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{},
				LabelEvents: map[string]time.Time{},
			},
			wantLen:    1,
			wantType:   []ActionType{ActionSkip},
			wantLabels: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := syncer.Reconcile(tt.item)

			if got := len(actions); got != tt.wantLen {
				t.Errorf("got %d actions, want %d", got, tt.wantLen)
				for i, a := range actions {
					t.Logf("  action[%d]: %s - %s", i, a.Type, a.Detail)
				}
				return
			}

			for i, wantType := range tt.wantType {
				if actions[i].Type != wantType {
					t.Errorf("action[%d]: got type %s, want %s", i, actions[i].Type, wantType)
				}
			}

			for i, wantLabel := range tt.wantLabels {
				if wantLabel != "" && actions[i].Label != wantLabel {
					t.Errorf("action[%d]: got Label %q, want %q", i, actions[i].Label, wantLabel)
				}
			}

			for i, wantStatus := range tt.wantStatus {
				if wantStatus != "" && actions[i].StatusName != wantStatus {
					t.Errorf("action[%d]: got StatusName %q, want %q", i, actions[i].StatusName, wantStatus)
				}
			}
		})
	}
}

func TestFilterByPrefix(t *testing.T) {
	labels := []string{"status:Todo", "bug", "status:In Progress", "enhancement"}
	got := filterByPrefix(labels, "status:")
	if len(got) != 2 {
		t.Fatalf("got %d labels, want 2", len(got))
	}
	if got[0] != "status:Todo" || got[1] != "status:In Progress" {
		t.Errorf("got %v, want [status:Todo, status:In Progress]", got)
	}
}
