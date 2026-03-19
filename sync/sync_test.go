package sync

import (
	"strings"
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

	mapping := map[string][]string{
		"Todo":        {"todo"},
		"In Progress": {"in-progress"},
		"Done":        {"done"},
	}

	syncer, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

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
			wantLabels: []string{"in-progress"},
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
				Labels:      []string{"in-progress", "bug"},
				LabelEvents: map[string]time.Time{
					"in-progress": now,
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
				Labels:      []string{"in-progress"},
				LabelEvents: map[string]time.Time{
					"in-progress": later,
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
				Labels:      []string{"todo"},
				LabelEvents: map[string]time.Time{
					"todo": earlier,
				},
			},
			wantLen:    2,
			wantType:   []ActionType{ActionRemoveLabel, ActionAddLabel},
			wantLabels: []string{"todo", "in-progress"},
		},
		{
			name: "multiple mapped labels -> clean up, board wins",
			item: gh.ProjectItem{
				ItemID:      "item5",
				UpdatedAt:   now,
				BoardStatus: "In Progress",
				IssueNumber: 5,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"todo", "done", "bug"},
				LabelEvents: map[string]time.Time{
					"todo": earlier,
					"done": earlier,
				},
			},
			wantLen:    3, // remove todo, remove done, add in-progress
			wantType:   []ActionType{ActionRemoveLabel, ActionRemoveLabel, ActionAddLabel},
			wantLabels: []string{"todo", "done", "in-progress"},
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
		{
			name: "board status not in mapping -> skip",
			item: gh.ProjectItem{
				ItemID:      "item8",
				UpdatedAt:   now,
				BoardStatus: "Backlog",
				IssueNumber: 8,
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

func TestFilterToMapped(t *testing.T) {
	allMapped := []string{"todo", "in-progress", "done"}
	labels := []string{"todo", "bug", "in-progress", "enhancement"}
	got := filterToMapped(labels, allMapped)
	if len(got) != 2 {
		t.Fatalf("got %d labels, want 2", len(got))
	}
	if got[0] != "todo" || got[1] != "in-progress" {
		t.Errorf("got %v, want [todo, in-progress]", got)
	}
}

func TestReconcileMultiLabel(t *testing.T) {
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

	mapping := map[string][]string{
		"Todo":        {"todo"},
		"In Progress": {"in-progress", "active"},
		"Done":        {"done"},
	}

	syncer, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

	tests := []struct {
		name       string
		item       gh.ProjectItem
		wantLen    int
		wantType   []ActionType
		wantLabels []string
		wantStatus []string
	}{
		{
			name: "multi-label: board wins, no current labels",
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
			wantLen:    2,
			wantType:   []ActionType{ActionAddLabel, ActionAddLabel},
			wantLabels: []string{"in-progress", "active"},
		},
		{
			name: "multi-label: in sync",
			item: gh.ProjectItem{
				ItemID:      "item2",
				UpdatedAt:   now,
				BoardStatus: "In Progress",
				IssueNumber: 2,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"in-progress", "active", "bug"},
				LabelEvents: map[string]time.Time{
					"in-progress": now,
					"active":      now,
				},
			},
			wantLen:  1,
			wantType: []ActionType{ActionSkip},
		},
		{
			name: "multi-label: partially in sync, board wins",
			item: gh.ProjectItem{
				ItemID:      "item3",
				UpdatedAt:   now,
				BoardStatus: "In Progress",
				IssueNumber: 3,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"in-progress", "bug"},
				LabelEvents: map[string]time.Time{
					"in-progress": earlier,
				},
			},
			// Current mapped: [in-progress], expected: [in-progress, active]
			// Not sameSet. inferStatus("in-progress") = "In Progress" (same as board).
			// But labels don't fully match expected. Board is newer → board wins.
			// Remove in-progress, add in-progress + active.
			wantLen:    3,
			wantType:   []ActionType{ActionRemoveLabel, ActionAddLabel, ActionAddLabel},
			wantLabels: []string{"in-progress", "in-progress", "active"},
		},
		{
			name: "multi-label: conflict, labels win",
			item: gh.ProjectItem{
				ItemID:      "item4",
				UpdatedAt:   earlier,
				BoardStatus: "Todo",
				IssueNumber: 4,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"in-progress", "active"},
				LabelEvents: map[string]time.Time{
					"in-progress": later,
					"active":      now,
				},
			},
			wantLen:    1,
			wantType:   []ActionType{ActionUpdateBoard},
			wantStatus: []string{"In Progress"},
		},
		{
			name: "multi-label: conflict, board wins",
			item: gh.ProjectItem{
				ItemID:      "item5",
				UpdatedAt:   later,
				BoardStatus: "In Progress",
				IssueNumber: 5,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"todo"},
				LabelEvents: map[string]time.Time{
					"todo": earlier,
				},
			},
			wantLen:    3, // remove todo, add in-progress, add active
			wantType:   []ActionType{ActionRemoveLabel, ActionAddLabel, ActionAddLabel},
			wantLabels: []string{"todo", "in-progress", "active"},
		},
		{
			name: "labels from different statuses, board wins cleanup",
			item: gh.ProjectItem{
				ItemID:      "item6",
				UpdatedAt:   now,
				BoardStatus: "Todo",
				IssueNumber: 6,
				IssueState:  "OPEN",
				RepoOwner:   "owner",
				RepoName:    "repo",
				Labels:      []string{"in-progress", "done"},
				LabelEvents: map[string]time.Time{
					"in-progress": later,
					"done":        later,
				},
			},
			wantLen:    3, // remove in-progress, remove done, add todo
			wantType:   []ActionType{ActionRemoveLabel, ActionRemoveLabel, ActionAddLabel},
			wantLabels: []string{"in-progress", "done", "todo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := syncer.Reconcile(tt.item)

			if got := len(actions); got != tt.wantLen {
				t.Errorf("got %d actions, want %d", got, tt.wantLen)
				for i, a := range actions {
					t.Logf("  action[%d]: %s label=%q status=%q - %s", i, a.Type, a.Label, a.StatusName, a.Detail)
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

func TestSameSet(t *testing.T) {
	tests := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a", "b"}, []string{"a"}, false},
		{[]string{"a"}, []string{"a", "b"}, false},
	}
	for _, tt := range tests {
		if got := sameSet(tt.a, tt.b); got != tt.want {
			t.Errorf("sameSet(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestInferStatus(t *testing.T) {
	rm := map[string]string{
		"in-progress": "In Progress",
		"active":      "In Progress",
		"todo":        "Todo",
		"done":        "Done",
	}
	tests := []struct {
		labels []string
		want   string
	}{
		{nil, ""},
		{[]string{"in-progress"}, "In Progress"},
		{[]string{"in-progress", "active"}, "In Progress"},
		{[]string{"in-progress", "done"}, ""},    // different statuses
		{[]string{"unknown"}, ""},                // not in map
		{[]string{"in-progress", "unknown"}, ""}, // partial unknown
	}
	for _, tt := range tests {
		if got := inferStatus(tt.labels, rm); got != tt.want {
			t.Errorf("inferStatus(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

func TestValidateMapping_AllValid(t *testing.T) {
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
	s, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

	if err := s.validateMapping(); err != nil {
		t.Fatalf("expected no error for valid mapping, got: %v", err)
	}
}

func TestValidateMapping_Typo(t *testing.T) {
	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test Project",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "Backlog"},
			{ID: "opt2", Name: "Ready"},
			{ID: "opt3", Name: "In Progress"},
			{ID: "opt4", Name: "In Review"},
			{ID: "opt5", Name: "Done"},
		},
	}
	mapping := map[string][]string{
		"In Progres": {"in-progress"}, // typo!
		"Done":       {"done"},
	}
	s, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

	err := s.validateMapping()
	if err == nil {
		t.Fatal("expected error for typo in mapping")
	}
	if !strings.Contains(err.Error(), "1 invalid mapping value(s)") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateMapping_UnmappedOption(t *testing.T) {
	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test Project",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "Backlog"},
			{ID: "opt2", Name: "In Progress"},
			{ID: "opt3", Name: "Done"},
		},
	}
	// Only map two of the three options; "Backlog" is unmapped.
	mapping := map[string][]string{
		"In Progress": {"in-progress"},
		"Done":        {"done"},
	}
	s, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

	// Unmapped options produce warnings, not errors.
	if err := s.validateMapping(); err != nil {
		t.Fatalf("unmapped options should warn, not error; got: %v", err)
	}
}

func TestValidateMapping_AllInvalid(t *testing.T) {
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
	// Every mapping key is wrong.
	mapping := map[string][]string{
		"To Do":      {"todo"},
		"In Progres": {"in-progress"},
		"Dne":        {"done"},
	}
	s, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

	err := s.validateMapping()
	if err == nil {
		t.Fatal("expected error when all mapping values are invalid")
	}
	if !strings.Contains(err.Error(), "3 invalid mapping value(s)") {
		t.Errorf("expected 3 errors, got: %v", err)
	}
}

func TestValidateMapping_CaseSensitive(t *testing.T) {
	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test Project",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "In Progress"},
		},
	}
	mapping := map[string][]string{
		"in progress": {"in-progress"}, // wrong case
	}
	s, _ := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "testowner", 1)

	err := s.validateMapping()
	if err == nil {
		t.Fatal("expected error for case mismatch")
	}
	if !strings.Contains(err.Error(), "1 invalid mapping value(s)") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNewSyncer_RejectsDuplicateLabels(t *testing.T) {
	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test Project",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "Ready"},
			{ID: "opt2", Name: "In Progress"},
			{ID: "opt3", Name: "Done"},
		},
	}

	t.Run("same label for two statuses is rejected", func(t *testing.T) {
		mapping := map[string][]string{
			"Ready":       {"in-progress"},
			"In Progress": {"in-progress"}, // duplicate!
			"Done":        {"done"},
		}
		_, err := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "owner", 1)
		if err == nil {
			t.Fatal("expected error for duplicate label across statuses, got nil")
		}
		if !strings.Contains(err.Error(), "in-progress") {
			t.Errorf("error should mention the duplicate label, got: %s", err)
		}
		if !strings.Contains(err.Error(), "same label cannot be used") {
			t.Errorf("error should explain the problem, got: %s", err)
		}
	})

	t.Run("unique labels are accepted", func(t *testing.T) {
		mapping := map[string][]string{
			"Ready":       {"ready"},
			"In Progress": {"in-progress"},
			"Done":        {"done"},
		}
		syncer, err := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "owner", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if syncer == nil {
			t.Fatal("expected syncer, got nil")
		}
	})

	t.Run("same label in same status is fine", func(t *testing.T) {
		// This shouldn't happen in practice but isn't a conflict
		mapping := map[string][]string{
			"In Progress": {"in-progress", "active"},
			"Done":        {"done"},
		}
		_, err := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "owner", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestLatestLabelTime(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	events := map[string]time.Time{
		"a": t1,
		"b": t3,
		"c": t2,
	}

	got := latestLabelTime([]string{"a", "b", "c"}, events)
	if !got.Equal(t3) {
		t.Errorf("got %v, want %v", got, t3)
	}

	// Labels not in events → zero time.
	got = latestLabelTime([]string{"x"}, events)
	if !got.IsZero() {
		t.Errorf("got %v, want zero time", got)
	}
}

func TestReconcile_LabelsWithSpaces(t *testing.T) {
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)

	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "In Progress"},
			{ID: "opt2", Name: "Done"},
		},
	}

	mapping := map[string][]string{
		"In Progress": {"In Progress"},    // label has space, same as status
		"Done":        {"Done & Shipped"}, // label has space and special char
	}

	syncer, err := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "owner", 1)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("board wins adds label with space", func(t *testing.T) {
		item := gh.ProjectItem{
			ItemID:      "item1",
			UpdatedAt:   now,
			BoardStatus: "In Progress",
			IssueNumber: 1,
			IssueState:  "OPEN",
			RepoOwner:   "owner",
			RepoName:    "repo",
			Labels:      []string{"bug"},
			LabelEvents: map[string]time.Time{},
		}
		actions := syncer.Reconcile(item)
		if len(actions) != 1 {
			t.Fatalf("got %d actions, want 1", len(actions))
		}
		if actions[0].Type != ActionAddLabel {
			t.Errorf("got %s, want add-label", actions[0].Type)
		}
		if actions[0].Label != "In Progress" {
			t.Errorf("label: got %q, want %q", actions[0].Label, "In Progress")
		}
	})

	t.Run("in sync with spaced label", func(t *testing.T) {
		item := gh.ProjectItem{
			ItemID:      "item2",
			UpdatedAt:   now,
			BoardStatus: "Done",
			IssueNumber: 2,
			IssueState:  "OPEN",
			RepoOwner:   "owner",
			RepoName:    "repo",
			Labels:      []string{"Done & Shipped"},
			LabelEvents: map[string]time.Time{
				"Done & Shipped": now,
			},
		}
		actions := syncer.Reconcile(item)
		if len(actions) != 1 || actions[0].Type != ActionSkip {
			t.Errorf("expected skip (in sync), got %v", actions)
		}
	})
}

func TestLogConfigSummary_ShowsUnmappedStatuses(t *testing.T) {
	project := &gh.ProjectInfo{
		ID:      "PVT_test",
		Title:   "Test Project",
		FieldID: "PVTSSF_test",
		Options: []gh.StatusOption{
			{ID: "opt1", Name: "Backlog"},
			{ID: "opt2", Name: "Ready"},
			{ID: "opt3", Name: "In Progress"},
			{ID: "opt4", Name: "QA"},
			{ID: "opt5", Name: "Done"},
		},
	}

	mapping := map[string][]string{
		"In Progress": {"in-progress"},
		"Done":        {"done"},
	}

	syncer, err := NewSyncer(project, nil, nil, nil, mapping, "Status", false, "owner", 1)
	if err != nil {
		t.Fatal(err)
	}

	// Verify unmapped statuses are tracked
	unmapped := syncer.UnmappedStatuses()
	if len(unmapped) != 3 {
		t.Fatalf("expected 3 unmapped statuses, got %d: %v", len(unmapped), unmapped)
	}
	// Should contain Backlog, Ready, QA but not In Progress or Done
	want := map[string]bool{"Backlog": true, "Ready": true, "QA": true}
	for _, s := range unmapped {
		if !want[s] {
			t.Errorf("unexpected unmapped status: %q", s)
		}
	}
}
